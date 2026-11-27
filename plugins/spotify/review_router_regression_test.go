package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

func TestOpenRouterEnsemblePreservesNormalizationAndSharedResearch(t *testing.T) {
	for _, tc := range []struct{ provider, configured, want string }{
		{"openrouter", "gpt-6.1-sol", "openai/gpt-6.1-sol"},
		{"openrouter", "anthropic/claude-opus-5.5:online", "anthropic/claude-opus-5.5"},
		{"openai-codex", "openrouter:anthropic/claude-opus-5.5:online", "anthropic/claude-opus-5.5"},
		{"openai-codex", "openrouter:gpt-6.1-sol", "openai/gpt-6.1-sol"},
	} {
		t.Run(tc.provider+"/"+tc.configured, func(t *testing.T) {
			id := setupOrca(t)
			if err := registry.SetAiProvider(id, tc.provider); err != nil {
				t.Fatal(err)
			}
			calls := 0
			http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "openrouter.ai" || r.URL.Path != "/api/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer wrong-openrouter-key" {
					t.Fatalf("wrong route/auth: %s", r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != tc.want {
					t.Errorf("model=%v want=%s", body["model"], tc.want)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"fixture"}}]}`))}, nil
			})
			if _, err := completeSpotifyReviewModel(context.Background(), id, tc.configured, openai.ChatCompletionRequest{}); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}
