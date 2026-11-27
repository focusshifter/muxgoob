package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

func TestMixedCodexOrcaAuthorsAndJudge(t *testing.T) {
	id := setupOrca(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"fixture-codex"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ configured, host, wire, key string }{
		{"openai/gpt-6.1-sol", "chatgpt.com", "gpt-6.1-sol", "fixture-codex"},
		{"anthropic/claude-opus-5.5", "api.orcarouter.ai", "anthropic/claude-opus-5.5", "orca-test"},
		{"orcarouter:anthropic/judge", "api.orcarouter.ai", "anthropic/judge", "orca-test"},
		{"openrouter:vendor/judge", "openrouter.ai", "vendor/judge", "wrong-openrouter-key"},
	} {
		t.Run(tc.configured, func(t *testing.T) {
			calls := 0
			http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != tc.host || r.Header.Get("Authorization") != "Bearer "+tc.key {
					t.Fatalf("wrong route/auth: %s", r.URL)
				}
				var req struct{ Model string }
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.Model != tc.wire {
					t.Fatalf("model=%q", req.Model)
				}
				body := `{"choices":[{"message":{"role":"assistant","content":"fixture"}}]}`
				if tc.host == "chatgpt.com" {
					body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"fixture\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"fixture\"}]}}\n\ndata: {\"type\":\"response.completed\"}\n\n"
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			for _, role := range []string{"author", "judge"} {
				_, err := completeSpotifyReviewModel(context.Background(), id, tc.configured, openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{{Role: "user", Content: role}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
			if got := spotifyReviewGeneratedModelID(tc.configured, tc.wire); got != tc.configured {
				t.Fatalf("provenance=%q", got)
			}
		})
	}
}

func TestEmptyModelRouteFailsBeforeSpotifyNetwork(t *testing.T) {
	id := setupOrca(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = orcaTransport(func(*http.Request) (*http.Response, error) { t.Fatal("empty route reached network"); return nil, nil })
	for _, model := range []string{"orcarouter:", "openrouter:"} {
		if _, err := completeSpotifyReviewModel(context.Background(), id, model, openai.ChatCompletionRequest{}); err == nil {
			t.Fatal("empty route accepted")
		}
	}
}
