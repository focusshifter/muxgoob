package spotify

import (
	"context"
	"encoding/json"
	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	openai "github.com/sashabaranov/go-openai"
	"io"
	"net/http"
	"strings"
	"testing"
)

type allmodelsTransport func(*http.Request) (*http.Response, error)

func (f allmodelsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func setupAllModels(t *testing.T) *int64 {
	t.Helper()
	oldDB, oldConfig, oldTransport := database.DB, registry.Config, http.DefaultTransport
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() {
		db.Close()
		database.DB = oldDB
		registry.Config = oldConfig
		http.DefaultTransport = oldTransport
	})
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	registry.Config.AiProvider = "openrouter"
	registry.Config.AiModel = "wrong-global"
	registry.Config.OpenrouterApiKey = "wrong-openrouter-key"
	registry.Config.AllmodelsApiKey = "allmodels-test"
	chatID := int64(92834)
	if err := registry.SetAiProvider(&chatID, "allmodels"); err != nil {
		t.Fatal(err)
	}
	return &chatID
}
func interceptAllModels(t *testing.T, model string) *int {
	t.Helper()
	calls := new(int)
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.URL.Host != "api.allmodels.io" || r.URL.Path != "/oai/chat/completions" || r.Header.Get("Authorization") != "Bearer allmodels-test" {
			t.Fatalf("wrong route/auth %s %q", r.URL, r.Header.Get("Authorization"))
		}
		var req struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != model {
			t.Fatalf("model=%q want=%q", req.Model, model)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"fixture reply"}}]}`))}, nil
	})
	return calls
}

func TestAllModelsEnsembleWriterJudgeRouting(t *testing.T) {
	id := setupAllModels(t)
	for _, model := range []string{"vendor/writer", "vendor/judge", "allmodels/auto", "allmodels/free"} {
		calls := interceptAllModels(t, model)
		if _, err := completeSpotifyReviewModel(context.Background(), id, model, openai.ChatCompletionRequest{}); err != nil {
			t.Fatal(err)
		}
		registry.Config.AllmodelsApiKey = ""
		if _, err := completeSpotifyReviewModel(context.Background(), id, model, openai.ChatCompletionRequest{}); err == nil {
			t.Fatal("missing key accepted")
		}
		if *calls != 1 {
			t.Fatalf("calls=%d", *calls)
		}
		registry.Config.AllmodelsApiKey = "allmodels-test"
	}
}

func TestAllModelsSingleSpotifyWriterRouting(t *testing.T) {
	for _, configured := range []string{"allmodels:deepseek/deepseek-v4.1-flash-abliterated", "allmodels:deepseek/deepseek-v4.1-flash-abliterated:online", "allmodels:allmodels/free", "allmodels:gpt-5.4"} {
		t.Run(configured, func(t *testing.T) { testAllModelsSingleWriter(t, configured) })
	}
}
func testAllModelsSingleWriter(t *testing.T, configured string) {
	id := setupAllModels(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	if err := SetReviewModels(id, []string{configured}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAiModel(id, "allmodels/free"); err != nil {
		t.Fatal(err)
	}
	calls := interceptAllModels(t, strings.TrimSuffix(strings.TrimPrefix(configured, "allmodels:"), ":online"))
	base := http.DefaultTransport
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(configured, ":online") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
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
			r.Body = io.NopCloser(strings.NewReader(string(body)))
		}
		resp, err := base.RoundTrip(r)
		if err == nil {
			resp.Body = io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"{\"review_text\":\"fixture review\",\"album_rating\":7}"}}]}`))
		}
		return resp, err
	})
	review, rating, model, err := callChatModelForReviewContext(context.Background(), id, "fixture prompt", "album")
	if err != nil || !strings.Contains(review, "fixture review") || !rating.Valid || rating.Float64 != 7 || model != configured {
		t.Fatalf("review=%q rating=%+v model=%q err=%v", review, rating, model, err)
	}
	registry.Config.AllmodelsApiKey = ""
	_, _, _, err = callChatModelForReviewContext(context.Background(), id, "fixture prompt", "album")
	if err == nil || !strings.Contains(err.Error(), "allmodels api key") {
		t.Fatalf("missing key: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("calls=%d", *calls)
	}
}

func TestAllModelsSelectionKeepsPublishedSonarResearchOnOpenRouter(t *testing.T) {
	setupAllModels(t)
	registry.Config.AiProvider = "allmodels"
	calls := 0
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "openrouter.ai" || r.URL.Path != "/api/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer wrong-openrouter-key" {
			t.Fatalf("research rerouted or credentials mixed: %s", r.URL)
		}
		var req struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "perplexity/sonar" {
			t.Fatalf("research model=%s", req.Model)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"sources\":[]}"}}]}`))}, nil
	})
	fetchPerplexityGroundingContext(context.Background(), "track", "Fixture Artist", "Fixture Track", "2026", nil)
	if calls != 1 {
		t.Fatalf("research calls=%d", calls)
	}
	registry.Config.OpenrouterApiKey = ""
	fetchPerplexityGroundingContext(context.Background(), "track", "Fixture Artist", "Fixture Track", "2026", nil)
	if calls != 1 {
		t.Fatal("research borrowed AllModels credentials")
	}
}
