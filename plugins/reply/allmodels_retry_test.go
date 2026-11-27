package reply

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

func TestAllModelsRetryPreservesExecutedToolHistory(t *testing.T) {
	for _, outcome := range []string{"success", "empty", "no_eligible"} {
		t.Run(outcome, func(t *testing.T) {
			id := setupAllModels(t)
			oldSQLite := sqliteDb
			sqliteDb = database.DB
			t.Cleanup(func() { sqliteDb = oldSQLite })
			promptmgr.EnsureTables()
			if _, err := database.DB.Exec(`CREATE TABLE IF NOT EXISTS messages (id INTEGER,chat_id INTEGER,unixtime INTEGER)`); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetAiModel(id, "allmodels:deepseek/deepseek-v4.1-flash-abliterated"); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetImageGenerationEnabled(*id, true); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetImagePromptMode(id, "fallback"); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetImagePromptProvider(id, "allmodels"); err != nil {
				t.Fatal(err)
			}
			if err := registry.SetImagePromptModel(id, "fixture/composer"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			var prior map[string]any
			http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if calls > 3 {
					t.Fatal("exceeded one upstream retry or invoked image composer")
				}
				if r.URL.Host != "api.allmodels.io" || body["model"] != "deepseek/deepseek-v4.1-flash-abliterated" {
					t.Fatal("changed model or router")
				}
				provider := body["provider"].(map[string]any)
				if provider["zdr"] != true {
					t.Fatal("ZDR removed")
				}
				if calls < 3 && provider["ignore"] != nil {
					t.Fatal("excluded upstream before empty reply")
				}
				response := `{"provider":"first","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"stable-read","type":"function","function":{"name":"getChatHistoryBounds","arguments":"{}"}}]}}]}`
				if calls == 2 {
					toolResults := 0
					for _, raw := range body["messages"].([]any) {
						m := raw.(map[string]any)
						if m["role"] == "tool" {
							toolResults++
							if m["tool_call_id"] != "stable-read" || !strings.Contains(m["content"].(string), `"count":0`) {
								t.Fatal("lost executed tool result")
							}
						}
					}
					if toolResults != 1 {
						t.Fatal("tool must execute once before continuation")
					}
					delete(body, "provider")
					prior = body
					response = `{"provider":"first","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`
				}
				if calls == 3 {
					ignore, ok := provider["ignore"].([]any)
					if !ok || len(ignore) != 1 || ignore[0] != "first" {
						t.Fatal("retry did not exclude failed upstream")
					}
					delete(body, "provider")
					if !reflect.DeepEqual(prior, body) {
						t.Fatal("retry mutated history or request; possible repeated host tool execution")
					}
					if outcome == "no_eligible" {
						return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":"no_matching_provider"}}`))}, nil
					}
					response = `{"provider":"second","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"recovered fixture"}}]}`
					if outcome == "empty" {
						response = `{"provider":"second","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":""}}]}`
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			msg := &telebot.Message{ID: 20, Text: "hello fixture", Chat: &telebot.Chat{ID: *id}, Sender: &telebot.User{ID: 42, FirstName: "Fixture"}}
			got := askChatGptWithMode(msg, false, nil, false)
			want := "recovered fixture"
			if outcome != "success" {
				want = "Сервис модели не вернул готовый ответ. Запрос завершён с ошибкой."
			}
			if got != want || calls != 3 {
				t.Fatalf("reply=%q calls=%d want=%q/3", got, calls, want)
			}
		})
	}
}
