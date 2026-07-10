package spotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	chattools "github.com/focusshifter/muxgoob/internal/tools"
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

	// Prompt for final review.
	prompt := buildSpotifyReviewPrompt(typ, artist, title, year, grounding)
	if prompt == "" {
		return ""
	}

	// Call for the actual review text
	review, rating := callChatModelForReview(&chatID, prompt, typ)
	if strings.TrimSpace(review) == "" {
		return ""
	}

	if err := saveReviewText(typ, spotifyID, review, rating); err != nil {
		log.Printf("[spotify] Failed to save review text: %v", err)
	}

	// Publish
	pageURL, err := publishToTelegraph(artist, title, year, canonicalSpotifyURL(typ, spotifyID), review)
	if err != nil {
		log.Printf("[spotify] Telegraph publish failed: %v", err)
		return ""
	}

	// Save the review URL to database for future reuse
	if err := saveReviewURL(typ, spotifyID, pageURL); err != nil {
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

	query := fmt.Sprintf("Give me a short consensus summary of reviews for %s - %s (%s). Include what critics and listeners most often praise, what they most often criticize, and any notable split in opinion. Keep it concise and factual.", artist, title, year)

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
		base = "Write a witty review in RUSSIAN about the {type} \"{title}\" ({year}) by {artist}. Semi-follow the overall consensus from the facts below: if reception is mixed, sound mixed; if it is positive or negative, lean that way without becoming bland. Do not be automatically harsh. Be sharp, observant, and occasionally funny, but avoid repetitive insults and generic takedowns. Mention both what works and what does not, focusing on concrete musical qualities such as songwriting, pacing, arrangements, melodies, atmosphere, or structure. Do not quote or cite the facts verbatim. No Markdown, use plain text ONLY, without '*', '«', '»' or '—'.\n\n{grounding}"
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
	if typ == "album" {
		prompt = prompt + "\n\nGive the album a numeric rating from 1 to 10 in 0.5 increments. The model must return this rating in the structured album_rating field. Do not include the numeric rating in review_text; the application will append the final rating paragraph."
	}
	return prompt
}

type spotifyStructuredReview struct {
	ReviewText  string   `json:"review_text"`
	AlbumRating *float64 `json:"album_rating"`
}

func buildSpotifyReviewCompletionRequest(model, prompt, typ string) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature:    float32(0.8),
		ResponseFormat: spotifyReviewResponseFormat(typ),
	}
}

func spotifyReviewResponseFormat(_ string) *openai.ChatCompletionResponseFormat {
	required := `["review_text","album_rating"]`
	schema := json.RawMessage(fmt.Sprintf(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"review_text":{"type":"string","description":"The review body without the final numeric rating paragraph."},
			"album_rating":{"type":["number","null"],"minimum":1,"maximum":10,"multipleOf":0.5,"description":"Album rating from 1 to 10 in 0.5 increments. Must be null for non-album reviews."}
		},
		"required":%s
	}`, required))
	return &openai.ChatCompletionResponseFormat{
		Type: openai.ChatCompletionResponseFormatTypeJSONSchema,
		JSONSchema: &openai.ChatCompletionResponseFormatJSONSchema{
			Name:   "spotify_review",
			Strict: true,
			Schema: schema,
		},
	}
}

func parseSpotifyReviewCompletion(content, typ string) (string, sql.NullFloat64, error) {
	var structured spotifyStructuredReview
	if err := json.Unmarshal([]byte(content), &structured); err != nil {
		return "", sql.NullFloat64{}, err
	}

	reviewText := strings.TrimSpace(structured.ReviewText)
	if reviewText == "" {
		return "", sql.NullFloat64{}, fmt.Errorf("empty review_text")
	}

	if typ != "album" {
		return reviewText, sql.NullFloat64{}, nil
	}
	if structured.AlbumRating == nil {
		return "", sql.NullFloat64{}, fmt.Errorf("missing album_rating")
	}
	rating := *structured.AlbumRating
	if !isValidAlbumRating(rating) {
		return "", sql.NullFloat64{}, fmt.Errorf("invalid album_rating: %v", rating)
	}
	return reviewText + "\n\n" + formatAlbumRating(rating), sql.NullFloat64{Float64: rating, Valid: true}, nil
}

func isValidAlbumRating(rating float64) bool {
	return rating >= 1 && rating <= 10 && math.Abs(rating*2-math.Round(rating*2)) < 0.000001
}

func formatAlbumRating(rating float64) string {
	formatted := fmt.Sprintf("%.1f", rating)
	if strings.HasSuffix(formatted, ".0") {
		formatted = strings.TrimSuffix(formatted, ".0")
	}
	formatted = strings.ReplaceAll(formatted, ".", ",")
	return formatted + " / 10"
}

func callChatModelForReview(chatID *int64, prompt, typ string) (string, sql.NullFloat64) {
	var client chattools.ChatCompletionCreator
	model := resolveSpotifyReviewModel(chatID)

	provider := registry.GetAiProvider(chatID)
	switch provider {
	case "openrouter":
		if registry.Config.OpenrouterApiKey == "" {
			return "", sql.NullFloat64{}
		}
		config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		model = strings.TrimSuffix(model, ":online")
		if model == "" {
			model = "deepseek/deepseek-chat-v3.1"
		}
		client = openai.NewClientWithConfig(config)
	case "openai-codex":
		modelInfo := openaicodex.NormalizeConfiguredModel(model)
		if modelInfo.Model == "" {
			modelInfo = openaicodex.NormalizeConfiguredModel("gpt-5.4")
		}
		model = modelInfo.Model
		if modelInfo.UseCodex {
			fallbackConfig := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
			fallbackConfig.BaseURL = "https://openrouter.ai/api/v1"
			client = openaicodex.NewClient(openaicodex.WithFallbackClient(openai.NewClientWithConfig(fallbackConfig)))
			model = modelInfo.RawModel
		} else {
			config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
			config.BaseURL = "https://openrouter.ai/api/v1"
			client = openai.NewClientWithConfig(config)
			model = modelInfo.OpenRouterModel
		}
	default:
		if registry.Config.OpenaiApiKey == "" {
			return "", sql.NullFloat64{}
		}
		config := openai.DefaultConfig(registry.Config.OpenaiApiKey)
		if model == "" {
			model = "gpt-4o-mini"
		}
		client = openai.NewClientWithConfig(config)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx, buildSpotifyReviewCompletionRequest(model, prompt, typ))
	if err != nil || len(resp.Choices) == 0 {
		if err != nil {
			log.Printf("[spotify] Chat review error: %v", err)
		}
		return "", sql.NullFloat64{}
	}
	review, rating, err := parseSpotifyReviewCompletion(resp.Choices[0].Message.Content, typ)
	if err != nil {
		log.Printf("[spotify] Failed to parse structured review response: %v", err)
		return "", sql.NullFloat64{}
	}
	return review, rating
}

func resolveSpotifyReviewModel(chatID *int64) string {
	model := GetReviewModel(chatID)
	if model != "" {
		return model
	}

	provider := registry.GetAiProvider(chatID)
	if provider == "openrouter" || provider == "openai-codex" {
		return strings.TrimSpace(registry.GetAiModel(chatID))
	}
	return "gpt-4o-mini"
}

type telegraphNode struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Children []interface{}     `json:"children"`
}

func canonicalSpotifyURL(typ, spotifyID string) string {
	return fmt.Sprintf("https://open.spotify.com/%s/%s", typ, spotifyID)
}

func buildTelegraphReviewContent(spotifyURL, review string) []telegraphNode {
	return []telegraphNode{
		{
			Tag: "p",
			Children: []interface{}{telegraphNode{
				Tag:      "a",
				Attrs:    map[string]string{"href": spotifyURL},
				Children: []interface{}{"Spotify"},
			}},
		},
		{Tag: "p", Children: []interface{}{review}},
	}
}

func publishToTelegraph(artist, title, year, spotifyURL, review string) (string, error) {
	accessToken := registry.Config.SpotifyReviewMicroblogAuth
	if accessToken == "" {
		return "", fmt.Errorf("no telegraph access token configured")
	}

	// See https://telegra.ph/api#createPage
	content := buildTelegraphReviewContent(spotifyURL, review)
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

func editTelegraph(existingURL, artist, title, year, spotifyURL, review string) error {
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

	content := buildTelegraphReviewContent(spotifyURL, review)
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
	return strings.TrimSpace(reviewURL)
}

// saveReviewText stores review text locally before publishing.
func saveReviewText(typ, itemKey, reviewText string, rating sql.NullFloat64) error {
	if strings.TrimSpace(reviewText) == "" {
		return nil
	}
	if typ != "album" {
		rating = sql.NullFloat64{}
	}

	var existingURL string
	err := database.DB.QueryRow(
		"SELECT review_url FROM spotify_reviews WHERE type = ? AND item_key = ?",
		typ, itemKey,
	).Scan(&existingURL)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	_, err = database.DB.Exec(
		"INSERT OR REPLACE INTO spotify_reviews (type, item_key, review_url, review_text, album_rating) VALUES (?, ?, ?, ?, ?)",
		typ, itemKey, strings.TrimSpace(existingURL), reviewText, rating,
	)
	return err
}

// saveReviewURL updates the review URL while preserving any stored review text.
func saveReviewURL(typ, itemKey, reviewURL string) error {
	if strings.TrimSpace(reviewURL) == "" {
		return nil
	}

	_, err := database.DB.Exec(
		"UPDATE spotify_reviews SET review_url = ? WHERE type = ? AND item_key = ?",
		reviewURL, typ, itemKey,
	)
	if err != nil {
		return err
	}

	// If there was no existing row, insert with empty review_text.
	res, err := database.DB.Exec(
		"INSERT OR IGNORE INTO spotify_reviews (type, item_key, review_url, review_text) VALUES (?, ?, ?, ?)",
		typ, itemKey, reviewURL, "",
	)
	if err != nil {
		return err
	}
	_ = res
	return nil
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

	review, rating := callChatModelForReview(&chatID, prompt, itemType)
	if strings.TrimSpace(review) == "" {
		return "", fmt.Errorf("failed to generate review")
	}

	if err := saveReviewText(itemType, spotifyID, review, rating); err != nil {
		log.Printf("[spotify] Failed to save review text: %v", err)
	}

	// Edit the existing Telegraph article
	if err := editTelegraph(reviewURL, artist, title, year, canonicalSpotifyURL(itemType, spotifyID), review); err != nil {
		return "", fmt.Errorf("failed to edit Telegraph article: %v", err)
	}

	log.Printf("[spotify] Successfully regenerated review for %s ID %s: %s", itemType, spotifyID, reviewURL)
	return reviewURL, nil
}
