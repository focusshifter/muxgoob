package reply

import (
	"context"
	"encoding/json"
	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"
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

func TestOrcaImagePromptComposerRouting(t *testing.T) {
	id := setupOrca(t)
	if err := registry.SetImagePromptProvider(id, "orcarouter"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImagePromptModel(id, "orcarouter/auto"); err != nil {
		t.Fatal(err)
	}
	calls := interceptOrca(t, "orcarouter/auto")
	client, model, ok := imagePromptComposer(id)
	if !ok {
		t.Fatal("not configured")
	}
	if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
		t.Fatal(err)
	}
	registry.Config.OrcarouterApiKey = ""
	client, model, ok = imagePromptComposer(id)
	if !ok {
		t.Fatal("must surface missing key through error client")
	}
	if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err == nil {
		t.Fatal("missing key accepted")
	}
	if *calls != 1 {
		t.Fatalf("calls=%d", *calls)
	}
}

func TestOrcaReplyToolLoop(t *testing.T) {
	for _, configured := range []string{"orcarouter/auto", "orcarouter:orcarouter/auto", "google/gemini-fixture:online"} {
		t.Run(configured, func(t *testing.T) { testOrcaReplyToolLoop(t, configured) })
	}
}
func testOrcaReplyToolLoop(t *testing.T, configured string) {
	id := setupOrca(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	wire := strings.TrimSuffix(strings.TrimPrefix(configured, "orcarouter:"), ":online")
	oldSQLite := sqliteDb
	sqliteDb = database.DB
	t.Cleanup(func() { sqliteDb = oldSQLite })
	promptmgr.EnsureTables()
	if _, err := database.DB.Exec(`CREATE TABLE IF NOT EXISTS messages (id INTEGER,chat_id INTEGER,unixtime INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAiModel(id, configured); err != nil {
		t.Fatal(err)
	}
	calls := 0
	http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.orcarouter.ai" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer orca-test" {
			t.Fatalf("wrong route/auth %s", r.URL)
		}
		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(configured, ":online") {
			searchTools := 0
			for _, tool := range req.Tools {
				if tool.Type == "function" && tool.Function != nil && tool.Function.Name == "googleSearch" {
					searchTools++
				}
			}
			if searchTools != 1 {
				t.Fatalf("native googleSearch tools=%d want=1", searchTools)
			}
		}
		historyTool := false
		for _, tool := range req.Tools {
			if tool.Type == "function" && tool.Function != nil && tool.Function.Name == "getChatHistoryBounds" {
				historyTool = true
			}
		}
		if req.Model != wire || !historyTool {
			t.Fatalf("lost model/host tools: %+v", req)
		}
		body := `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"fixture-call","type":"function","function":{"name":"getChatHistoryBounds","arguments":"{}"}}]}}]}`
		if calls == 2 {
			found := false
			for _, m := range req.Messages {
				if m.Role == "tool" && m.ToolCallID == "fixture-call" {
					if !strings.Contains(m.Content, `"count":0`) {
						t.Fatalf("tool execution failed: %s", m.Content)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("tool result not continued")
			}
			body = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"fixture reply"}}]}`
		}
		if calls > 2 {
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	msg := &telebot.Message{ID: 20, Text: "hello fixture", Chat: &telebot.Chat{ID: *id}, Sender: &telebot.User{ID: 42, FirstName: "Fixture"}}
	if got := askChatGptWithMode(msg, false, nil, false); got != "fixture reply" {
		t.Fatalf("reply=%q", got)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
	registry.Config.OrcarouterApiKey = ""
	if got := askChatGptWithMode(msg, false, nil, false); got != "" {
		t.Fatalf("unexpected missing-key reply=%q", got)
	}
	if calls != 2 {
		t.Fatal("missing key reached network")
	}
}
