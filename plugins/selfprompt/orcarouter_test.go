package selfprompt

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

type orcaTransport func(*http.Request) (*http.Response, error)

func (f orcaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func setupOrca(t *testing.T) *int64 {
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
	registry.Config.OrcarouterApiKey = "orca-test"
	chatID := int64(92834)
	if err := registry.SetAiProvider(&chatID, "orcarouter"); err != nil {
		t.Fatal(err)
	}
	return &chatID
}
func interceptOrca(t *testing.T, model string) *int {
	t.Helper()
	calls := new(int)
	http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.URL.Host != "api.orcarouter.ai" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer orca-test" {
			t.Fatalf("wrong route/auth %s %q", r.URL, r.Header.Get("Authorization"))
		}
		var req openai.ChatCompletionRequest
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

func TestOrcaSelfpromptCompressionRouting(t *testing.T) {
	id := setupOrca(t)
	for _, model := range []string{"vendor/model", "orcarouter/auto", "orcarouter/free"} {
		calls := interceptOrca(t, model)
		client, actual := buildOpenAIClientForModel(id, model)
		if actual != model {
			t.Fatalf("model=%s", actual)
		}
		if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: actual}); err != nil {
			t.Fatal(err)
		}
		registry.Config.OrcarouterApiKey = ""
		client, actual = buildOpenAIClientForModel(id, model)
		if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: actual}); err == nil {
			t.Fatal("missing key accepted")
		}
		if *calls != 1 {
			t.Fatalf("calls=%d", *calls)
		}
		registry.Config.OrcarouterApiKey = "orca-test"
	}
}
