// Package aiprovider defines the OpenAI-compatible router boundary. Models are
// opaque provider IDs: in particular orcarouter/auto and orcarouter/free are native.
package aiprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/focusshifter/muxgoob/internal/allmodels"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/internal/orcarouter"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

// Resolve splits only a recognized routing prefix, exactly once. All other
// model tokens (including native slash IDs and :online) remain opaque. Unknown
// prefixes never select a provider, endpoint, or credentials.
func Resolve(provider, model string) (string, string, error) {
	for _, route := range []string{AllModels, OrcaRouter, "openrouter"} {
		if strings.HasPrefix(model, route+":") {
			id := strings.TrimPrefix(model, route+":")
			if strings.TrimSpace(id) == "" {
				return "", "", fmt.Errorf("%s model ID is empty", route)
			}
			return route, id, nil
		}
	}
	if provider == "openai-codex" && strings.HasPrefix(model, "openrouter/") {
		return "openrouter", strings.TrimPrefix(model, "openrouter/"), nil
	}
	if provider == "openai-codex" && strings.TrimSpace(model) != "" && !openaicodex.NormalizeConfiguredModel(model).UseCodex && !strings.HasPrefix(model, "openai/") {
		return OrcaRouter, model, nil
	}
	return provider, model, nil
}

// ErrorClient fails before network IO for callers with legacy client signatures.
func ErrorClient(err error) Client { return unavailable{err} }

const AllModels = "allmodels"
const OrcaRouter = "orcarouter"
const OrcaRouterBaseURL = "https://api.orcarouter.ai/v1"

type Client interface {
	CreateChatCompletion(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

type unavailable struct{ err error }

func (c unavailable) CreateChatCompletion(context.Context, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	return openai.ChatCompletionResponse{}, c.err
}

// Router never borrows credentials or falls back to another provider. The error
// client preserves callers' existing interfaces while failing before any network IO.
func Router(provider string) Client {
	if provider == AllModels {
		if strings.TrimSpace(registry.Config.AllmodelsApiKey) == "" {
			return unavailable{fmt.Errorf("allmodels api key is not configured")}
		}
		return allmodels.NewClient(registry.Config.AllmodelsApiKey)
	}
	key, base := registry.Config.OpenrouterApiKey, "https://openrouter.ai/api/v1"
	if provider == OrcaRouter {
		key, base = registry.Config.OrcarouterApiKey, OrcaRouterBaseURL
		if strings.TrimSpace(key) == "" {
			return unavailable{fmt.Errorf("orcarouter api key is not configured")}
		}
		return orcarouter.NewClient(key)
	} else if provider != "openrouter" {
		return unavailable{fmt.Errorf("unsupported router provider %q", provider)}
	}
	cfg := openai.DefaultConfig(key)
	cfg.BaseURL = base
	return openai.NewClientWithConfig(cfg)
}

func IsRouter(provider string) bool {
	return provider == "openrouter" || provider == OrcaRouter || provider == AllModels
}
func IsChatProvider(provider string) bool {
	return IsRouter(provider) || provider == "openai" || provider == "openai-codex"
}
func IsImageProvider(provider string) bool {
	return provider == "openrouter" || provider == OrcaRouter || provider == "openai-codex"
}
