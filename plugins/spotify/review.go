package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"
)

// generateAndPublishReview builds a review using LLMs and publishes it
// Returns the Telegraph page URL or an empty string on failure
func generateAndPublishReview(chatID int64, typ, artist, title, year string) string {
	if registry.Config.SpotifyReviewMicroblogAuth == "" {
		return ""
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
		MaxTokens:   300,
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature: float32(0.8),
		MaxTokens:   500,
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
