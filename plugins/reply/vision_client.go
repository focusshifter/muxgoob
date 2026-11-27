package reply

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/focusshifter/muxgoob/internal/aiprovider"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

// Vision routing follows the chat's AI provider, but deliberately uses the
// independent vision model, not the main conversational or image-generation model.
func imageVisionClient(chatID int64) (openaicodex.ChatCompletionCreator, string, error) {
	model := strings.TrimSpace(registry.GetImageVisionModel(&chatID))
	if model == "" {
		model = defaultImageAiModel
	}
	provider, model, err := aiprovider.Resolve(registry.GetAiProvider(&chatID), model)
	if err != nil {
		return nil, "", err
	}
	if provider == aiprovider.AllModels {
		if strings.TrimSpace(registry.Config.AllmodelsApiKey) == "" {
			return nil, "", fmt.Errorf("allmodels api key is not configured")
		}
		return aiprovider.Router(provider), model, nil
	}
	if aiprovider.IsRouter(provider) {
		if provider == "openrouter" {
			if strings.TrimSpace(registry.Config.OpenrouterApiKey) == "" {
				return nil, "", fmt.Errorf("openrouter api key is not configured")
			}
			return aiprovider.Router(provider), model, nil
		}
		log.Printf("[reply/vision] chat=%d effective_provider=%s model=%s", chatID, provider, model)
		if strings.TrimSpace(registry.Config.OrcarouterApiKey) == "" {
			return nil, "", fmt.Errorf("orcarouter api key is not configured")
		}
		return aiprovider.Router(provider), model, nil
	}
	config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
	config.BaseURL = "https://openrouter.ai/api/v1"
	router := openai.NewClientWithConfig(config)
	if provider == "openai-codex" {
		info := openaicodex.NormalizeConfiguredModel(model)
		if info.UseCodex {
			// Use the same Codex fallback policy as replies. Missing router credentials
			// do not prevent subscription requests, and do not enable an unusable fallback.
			options := []openaicodex.Option{}
			if strings.TrimSpace(registry.Config.OpenrouterApiKey) != "" {
				options = append(options, openaicodex.WithFallbackClient(router))
			}
			log.Printf("[reply/vision] chat=%d configured_provider=%s effective_provider=openai-codex model=%s fallback_enabled=%t", chatID, provider, info.Model, len(options) > 0)
			return openaicodex.NewClient(options...), model, nil
		}
		model = info.OpenRouterModel
	}
	// Preserve the legacy OpenRouter route for other providers and non-Codex models.
	if strings.TrimSpace(registry.Config.OpenrouterApiKey) == "" {
		return nil, "", fmt.Errorf("openrouter api key is not configured")
	}
	log.Printf("[reply/vision] chat=%d configured_provider=%s effective_provider=openrouter model=%s", chatID, provider, model)
	return router, model, nil
}

func createImageVisionCompletion(ctx context.Context, chatID int64, build func(string) openai.ChatCompletionRequest) (openai.ChatCompletionResponse, string, error) {
	client, model, err := imageVisionClient(chatID)
	if err != nil {
		return openai.ChatCompletionResponse{}, "", err
	}
	response, err := client.CreateChatCompletion(ctx, build(model))
	return response, model, err
}
