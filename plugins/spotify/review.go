package spotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"
)

// generateAndPublishReview builds a review using LLMs and publishes it
// Returns the Telegraph page URL or an empty string on failure
func generateAndPublishReview(chatID int64, typ, spotifyID, artist, title, year string) string {
	if registry.Config.SpotifyReviewMicroblogAuth == "" {
		return ""
	}

	// Check if we already have a review for this item using Spotify ID
	if existingURL := getExistingReview(typ, spotifyID); existingURL != "" {
		log.Printf("[spotify] Reusing existing review for %s ID %s: %s", typ, spotifyID, existingURL)
		return existingURL
	}

	// Keep typing while we do our stuff
	stopTyping := withTyping(chatID)
	defer stopTyping()

	// Try to get grounded context
	grounding := fetchPerplexityGrounding(artist, title, year)

	// Prompt for final review using the same model as general chat
	prompt := buildSpotifyReviewPrompt(typ, artist, title, year, grounding)
	if prompt == "" {
		return ""
	}

	// Call for the actual review text
	review := callChatModelForReview(&chatID, prompt)
	if strings.TrimSpace(review) == "" {
		return ""
	}

	// Publish
	pageURL, err := publishToTelegraph(artist, title, year, review)
	if err != nil {
		log.Printf("[spotify] Telegraph publish failed: %v", err)
		return ""
	}

	// Save the review URL to database for future reuse
	if err := saveReview(typ, spotifyID, pageURL); err != nil {
		log.Printf("[spotify] Failed to save review to database: %v", err)
		// Don't fail the operation, just log the error
	}

	return pageURL
}

func fetchPerplexityGrounding(artist, title, year string) string {
	if registry.Config.OpenrouterApiKey == "" {
		return ""
	}

	// perplexity/sonar through OpenRouter is hardcoded
	config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
	config.BaseURL = "https://openrouter.ai/api/v1"
	client := openai.NewClientWithConfig(config)

	query := fmt.Sprintf("Give me short summary of reviews for %s - %s (%s)", artist, title, year)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: "perplexity/sonar",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: query},
		},
		Temperature: float32(0.2),
		MaxTokens:   600,
	})
	if err != nil || len(resp.Choices) == 0 {
		if err != nil {
			log.Printf("[spotify] Perplexity grounding error: %v", err)
		}
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

func buildSpotifyReviewPrompt(typ, artist, title, year, grounding string) string {
	base := registry.Config.SpotifyReviewPrompt
	if strings.TrimSpace(base) == "" {
		// Default prompt for fallback if not configured
		base = "Write rude and sarcastic review about the {type} \"{title}\" ({year}) by {artist} IN RUSSIAN. No Markdown, use plain text ONLY, without '*', '«', '»' or '—'. Use the facts below for context without quoting them:\n\n{grounding}"
	}

	repl := func(s, k, v string) string { return strings.ReplaceAll(s, k, v) }
	prompt := base
	prompt = repl(prompt, "{type}", typ)
	prompt = repl(prompt, "{artist}", artist)
	prompt = repl(prompt, "{title}", title)
	prompt = repl(prompt, "{year}", year)
	if strings.Contains(prompt, "{grounding}") {
		prompt = repl(prompt, "{grounding}", grounding)
	} else if grounding != "" {
		prompt = prompt + "\n\nGrounding (do not quote verbatim):\n" + grounding
	}
	return prompt
}

func callChatModelForReview(chatID *int64, prompt string) string {
	var config openai.ClientConfig
	var model string

	provider := registry.GetAiProvider(chatID)
	if provider == "openrouter" {
		if registry.Config.OpenrouterApiKey == "" {
			return ""
		}
		config = openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		model = registry.GetAiModel(chatID)
		if strings.TrimSpace(model) == "" {
			model = "deepseek/deepseek-chat"
		}
	} else {
		if registry.Config.OpenaiApiKey == "" {
			return ""
		}
		config = openai.DefaultConfig(registry.Config.OpenaiApiKey)
		model = "gpt-5"
	}

	client := openai.NewClientWithConfig(config)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature: float32(0.8),
	})
	if err != nil || len(resp.Choices) == 0 {
		if err != nil {
			log.Printf("[spotify] Chat review error: %v", err)
		}
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

func publishToTelegraph(artist, title, year, review string) (string, error) {
	accessToken := registry.Config.SpotifyReviewMicroblogAuth
	if accessToken == "" {
		return "", fmt.Errorf("no telegraph access token configured")
	}

	// Very simple paragraph node with the review text
	// See https://telegra.ph/api#createPage
	type node struct {
		Tag      string        `json:"tag"`
		Children []interface{} `json:"children"`
	}

	content := []node{{
		Tag:      "p",
		Children: []interface{}{review},
	}}
	contentJSON, _ := json.Marshal(content)

	var titleText string
	if strings.TrimSpace(year) != "" {
		titleText = fmt.Sprintf("%s — %s (%s)", artist, title, year)
	} else {
		titleText = fmt.Sprintf("%s — %s", artist, title)
	}
	form := url.Values{}
	form.Set("access_token", accessToken)
	form.Set("title", titleText)
	form.Set("author_name", "Губи")
	form.Set("return_content", "false")
	form.Set("content", string(contentJSON))

	req, err := http.NewRequest("POST", "https://api.telegra.ph/createPage", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
		Error string `json:"error"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&result); err != nil {
		return "", err
	}
	if !result.OK || result.Result.URL == "" {
		return "", fmt.Errorf("telegraph error: %s", result.Error)
	}
	return result.Result.URL, nil
}

func editTelegraph(existingURL, artist, title, year, review string) error {
	accessToken := registry.Config.SpotifyReviewMicroblogAuth
	if accessToken == "" {
		return fmt.Errorf("no telegraph access token configured")
	}

	// Extract path from the URL (e.g., "Article-Title-12-31" from "https://telegra.ph/Article-Title-12-31")
	parts := strings.Split(existingURL, "/")
	if len(parts) < 4 {
		return fmt.Errorf("invalid telegraph URL format")
	}
	path := parts[len(parts)-1]

	// Very simple paragraph node with the review text
	type node struct {
		Tag      string        `json:"tag"`
		Children []interface{} `json:"children"`
	}

	content := []node{{
		Tag:      "p",
		Children: []interface{}{review},
	}}
	contentJSON, _ := json.Marshal(content)

	var titleText string
	if strings.TrimSpace(year) != "" {
		titleText = fmt.Sprintf("%s — %s (%s)", artist, title, year)
	} else {
		titleText = fmt.Sprintf("%s — %s", artist, title)
	}
	
	form := url.Values{}
	form.Set("access_token", accessToken)
	form.Set("path", path)
	form.Set("title", titleText)
	form.Set("author_name", "Губи")
	form.Set("return_content", "false")
	form.Set("content", string(contentJSON))

	req, err := http.NewRequest("POST", "https://api.telegra.ph/editPage/"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
		Error string `json:"error"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegraph error: %s", result.Error)
	}
	return nil
}

// withTyping continuously sends "typing" until the returned stop function is called
func withTyping(chatID int64) func() {
	chat := &telebot.Chat{ID: chatID}
	// Fire immediately, then every 4 seconds (Telegram action lasts ~5s)
	_ = registry.Bot.Notify(chat, telebot.Typing)
	ticker := time.NewTicker(4 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				_ = registry.Bot.Notify(chat, telebot.Typing)
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	return func() { close(done) }
}

// getExistingReview checks if we already have a review for this item
func getExistingReview(typ, itemKey string) string {
	var reviewURL string
	err := database.DB.QueryRow(
		"SELECT review_url FROM spotify_reviews WHERE type = ? AND item_key = ?",
		typ, itemKey,
	).Scan(&reviewURL)

	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		log.Printf("[spotify] Failed to check for existing review: %v", err)
		return ""
	}
	return reviewURL
}

// saveReview saves a review URL to the database for future reuse
func saveReview(typ, itemKey, reviewURL string) error {
	_, err := database.DB.Exec(
		"INSERT OR REPLACE INTO spotify_reviews (type, item_key, review_url) VALUES (?, ?, ?)",
		typ, itemKey, reviewURL,
	)
	return err
}

// RegenerateReview regenerates a review for an existing Spotify item
func RegenerateReview(chatID int64, spotifyID string) (string, error) {
	// Check if we have an existing review
	var reviewURL string
	var itemType string
	
	// Try to find the item as an album first
	err := database.DB.QueryRow(
		"SELECT review_url FROM spotify_reviews WHERE item_key = ? AND type = 'album'",
		spotifyID,
	).Scan(&reviewURL)
	
	if err == sql.ErrNoRows {
		// Try as a track
		err = database.DB.QueryRow(
			"SELECT review_url FROM spotify_reviews WHERE item_key = ? AND type = 'track'",
			spotifyID,
		).Scan(&reviewURL)
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no existing review found for Spotify ID: %s", spotifyID)
		}
		itemType = "track"
	} else if err != nil {
		return "", fmt.Errorf("database error: %v", err)
	} else {
		itemType = "album"
	}

	// Keep typing while we do our stuff
	stopTyping := withTyping(chatID)
	defer stopTyping()
	
	// Fetch metadata from Spotify
	p := &SpotifyPlugin{}
	if err := p.EnsureAccessToken(); err != nil {
		return "", fmt.Errorf("failed to get Spotify access token: %v", err)
	}
	
	var artist, title, year string
	
	if itemType == "album" {
		album, err := p.FetchAlbum(spotifyID)
		if err != nil {
			return "", fmt.Errorf("failed to fetch album from Spotify: %v", err)
		}
		
		if len(album.Artists) > 0 {
			artist = album.Artists[0].Name
		}
		title = album.Name
		if len(album.ReleaseDate) >= 4 {
			year = album.ReleaseDate[:4]
		}
	} else {
		track, err := p.FetchTrack(spotifyID)
		if err != nil {
			return "", fmt.Errorf("failed to fetch track from Spotify: %v", err)
		}
		
		if len(track.Artists) > 0 {
			artist = track.Artists[0].Name
		}
		title = track.Name
		if len(track.Album.ReleaseDate) >= 4 {
			year = track.Album.ReleaseDate[:4]
		}
	}
	
	// Try to get grounded context
	grounding := fetchPerplexityGrounding(artist, title, year)
	
	// Generate new review
	prompt := buildSpotifyReviewPrompt(itemType, artist, title, year, grounding)
	if prompt == "" {
		return "", fmt.Errorf("failed to build review prompt")
	}
	
	review := callChatModelForReview(&chatID, prompt)
	if strings.TrimSpace(review) == "" {
		return "", fmt.Errorf("failed to generate review")
	}
	
	// Edit the existing Telegraph article
	if err := editTelegraph(reviewURL, artist, title, year, review); err != nil {
		return "", fmt.Errorf("failed to edit Telegraph article: %v", err)
	}
	
	log.Printf("[spotify] Successfully regenerated review for %s ID %s: %s", itemType, spotifyID, reviewURL)
	return reviewURL, nil
}
