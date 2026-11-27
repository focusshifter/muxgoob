package main

import (
	"context"
	"encoding/json"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCodexDefaultIndependentCompressionRoute(t *testing.T) {
	id := setupOrca(t)
	old := modelOverride
	modelOverride = ""
	t.Cleanup(func() { modelOverride = old })
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAiModel(id, "openai/gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	for _, configured := range []string{"anthropic/claude-sonnet-4:online", "orcarouter:orcarouter/free"} {
		rawModel := strings.TrimPrefix(configured, "orcarouter:")
		wire := strings.TrimSuffix(rawModel, ":online")
		calls := interceptOrca(t, wire)
		base := http.DefaultTransport
		http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(rawModel, ":online") {
				var req struct {
					WebSearchOptions struct {
						SearchContextSize string `json:"search_context_size"`
					} `json:"web_search_options"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatal(err)
				}
				if req.WebSearchOptions.SearchContextSize != "medium" {
					t.Fatalf("missing native search options: %s", body)
				}
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			return base.RoundTrip(r)
		})
		client, model := buildOpenAIClientForModel(id, configured)
		if model != rawModel {
			t.Fatalf("model=%q", model)
		}
		if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
			t.Fatal(err)
		}
		if err := registry.SetPluginSetting(id, "selfprompt", "model", configured); err != nil {
			t.Fatal(err)
		}
		ownClient, ownModel := buildOpenAIClient(id)
		if ownModel != rawModel {
			t.Fatalf("own model=%q", ownModel)
		}
		if _, err := ownClient.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: ownModel}); err != nil {
			t.Fatal(err)
		}
		if *calls != 2 {
			t.Fatalf("calls=%d", *calls)
		}
	}
	http.DefaultTransport = orcaTransport(func(*http.Request) (*http.Response, error) { t.Fatal("empty route reached network"); return nil, nil })
	client, model := buildOpenAIClientForModel(id, "orcarouter:")
	if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err == nil {
		t.Fatal("empty route accepted")
	}
}
