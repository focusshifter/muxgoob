package reply

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

func TestAllModelsEmptyReplyIsVisibleWithoutRetryOrComposer(t *testing.T) {
	for _, mode := range []string{"off", "fallback"} {
		for _, body := range []string{
			`{"id":"fixture-empty","provider":"fixture","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"","reasoning_content":"PRIVATE_REASONING"}}]}`,
			`{"choices":[]}`, `{"choices":null}`, `{}`,
		} {
			t.Run(mode+body, func(t *testing.T) { testAllModelsEmptyReply(t, mode, body, false) })
		}
	}
}

// The control proves this fixture really makes composer fallback eligible.
func TestAllModelsGenericErrorStillReachesConfiguredComposer(t *testing.T) {
	testAllModelsEmptyReply(t, "fallback", "", true)
}

func testAllModelsEmptyReply(t *testing.T, mode, fixture string, genericError bool) {
	t.Helper()
	id := setupAllModels(t)
	oldSQLite := sqliteDb
	sqliteDb = database.DB
	t.Cleanup(func() { sqliteDb = oldSQLite })
	promptmgr.EnsureTables()
	if _, err := database.DB.Exec(`CREATE TABLE IF NOT EXISTS messages (id INTEGER,chat_id INTEGER,unixtime INTEGER)`); err != nil {
		t.Fatal(err)
	}
	const configured = "allmodels:deepseek/deepseek-v4.1-flash-abliterated"
	if err := registry.SetAiModel(id, configured); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImagePromptMode(id, mode); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImagePromptProvider(id, "allmodels"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImagePromptModel(id, "fixture/composer"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImageGenerationEnabled(*id, mode == "fallback"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.URL.Host != "api.allmodels.io" || body["provider"].(map[string]any)["zdr"] != true {
			t.Fatal("wrong route or missing ZDR")
		}
		if calls == 1 {
			if body["model"] != strings.TrimPrefix(configured, "allmodels:") {
				t.Fatal("wrong reply model")
			}
			if mode == "fallback" {
				found := false
				for _, raw := range body["tools"].([]any) {
					tool := raw.(map[string]any)
					if tool["function"].(map[string]any)["name"] == "generateImage" {
						found = true
					}
				}
				if !found {
					t.Fatal("fixture did not enable image generation")
				}
			}
			if genericError {
				return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture failure"))}, nil
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fixture))}, nil
		}
		if !genericError || calls != 2 || body["model"] != "fixture/composer" {
			t.Fatal("empty response caused retry or unexpected composer fallback")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"fixture composer control"}}]}`))}, nil
	})
	question := "hello fixture"
	if mode == "fallback" {
		question = "нарисуй кота"
	}
	msg := &telebot.Message{ID: 20, Text: question, Chat: &telebot.Chat{ID: *id}, Sender: &telebot.User{ID: 42, FirstName: "Fixture"}}
	got := askChatGptWithMode(msg, false, nil, false)
	want := "Сервис модели вернул пустой ответ. Ответ не сформирован; автоматический повтор не выполнялся."
	count := 1
	if genericError {
		want = "fixture composer control"
		count = 2
	}
	if got != want {
		t.Fatalf("reply=%q want=%q", got, want)
	}
	if calls != count {
		t.Fatalf("calls=%d want=%d", calls, count)
	}
}
