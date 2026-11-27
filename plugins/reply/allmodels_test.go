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

func TestAllModelsImagePromptComposerRouting(t *testing.T) {
	id := setupAllModels(t)
	if err := registry.SetImagePromptProvider(id, "allmodels"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImagePromptModel(id, "allmodels/auto"); err != nil {
		t.Fatal(err)
	}
	calls := interceptAllModels(t, "allmodels/auto")
	client, model, ok := imagePromptComposer(id)
	if !ok {
		t.Fatal("not configured")
	}
	if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
		t.Fatal(err)
	}
	registry.Config.AllmodelsApiKey = ""
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

func TestAllModelsReplyToolLoop(t *testing.T) {
	for _, configured := range []string{"allmodels:deepseek/deepseek-v4.1-flash-abliterated", "allmodels:deepseek/deepseek-v4.1-flash-abliterated:online", "allmodels:allmodels/auto"} {
		t.Run(configured, func(t *testing.T) { testAllModelsReplyToolLoop(t, configured) })
	}
}
func testAllModelsReplyToolLoop(t *testing.T, configured string) {
	id := setupAllModels(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	wire := strings.TrimSuffix(strings.TrimPrefix(configured, "allmodels:"), ":online")
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
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.allmodels.io" || r.URL.Path != "/oai/chat/completions" || r.Header.Get("Authorization") != "Bearer allmodels-test" {
			t.Fatalf("wrong route/auth %s", r.URL)
		}
		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
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
	registry.Config.AllmodelsApiKey = ""
	if got := askChatGptWithMode(msg, false, nil, false); got != "" {
		t.Fatalf("unexpected missing-key reply=%q", got)
	}
	if calls != 2 {
		t.Fatal("missing key reached network")
	}
}

func TestAllModelsIndependentVisionAndComposerUnderCodex(t *testing.T) {
	id := setupAllModels(t)
	registry.SetAiProvider(id, "openai-codex")
	registry.SetAiModel(id, "openai/gpt-5.4")
	const configured = "allmodels:deepseek/deepseek-v4.1-flash-abliterated"
	registry.SetImageVisionModel(id, configured)
	registry.SetImagePromptProvider(id, "openai-codex")
	registry.SetImagePromptModel(id, configured)
	calls := interceptAllModels(t, strings.TrimPrefix(configured, "allmodels:"))
	client, model, err := imageVisionClient(*id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model, Messages: []openai.ChatCompletionMessage{{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,fixture"}}}}}}); err != nil {
		t.Fatal(err)
	}
	composer, m, ok := imagePromptComposer(id)
	if !ok {
		t.Fatal("composer unavailable")
	}
	if _, err = composer.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: m}); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("calls=%d", *calls)
	}
	registry.Config.AllmodelsApiKey = ""
	if _, _, err = imageVisionClient(*id); err == nil {
		t.Fatal("vision borrowed another key")
	}
}
