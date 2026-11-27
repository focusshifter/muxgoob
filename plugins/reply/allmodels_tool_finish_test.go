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

func TestAllModelsStopToolCallExecutesAndContinues(t *testing.T) {
	for _, afterRetry := range []bool{false, true} {
		name := "initial"
		if afterRetry {
			name = "alternate_upstream"
		}
		t.Run(name, func(t *testing.T) {
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
				toolStep, finalStep := 1, 2
				if afterRetry {
					toolStep = 2
					finalStep = 3
				}
				response := `{"provider":"first","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null}}]}`
				if calls == toolStep {
					provider := body["provider"].(map[string]any)
					if afterRetry {
						v, ok := provider["ignore"].([]any)
						if !ok || len(v) != 1 || v[0] != "first" {
							t.Fatal("missing upstream exclusion")
						}
					}
					response = `{"provider":"second","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null,"tool_calls":[{"id":"read-once","type":"function","function":{"name":"getChatHistoryBounds","arguments":"{}"}}]}}]}`
				}
				if calls == finalStep {
					found := 0
					for _, raw := range body["messages"].([]any) {
						m := raw.(map[string]any)
						if m["role"] == "tool" {
							found++
							if m["tool_call_id"] != "read-once" || !strings.Contains(m["content"].(string), `"count":0`) {
								t.Fatal("tool result missing or invalid")
							}
						}
					}
					if found != 1 {
						t.Fatal("tool must run once before continuation")
					}
					response = `{"provider":"second","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"fixture continued"}}]}`
				}
				if calls > finalStep {
					t.Fatal("unexpected extra completion")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			msg := &telebot.Message{ID: 20, Text: "hello fixture", Chat: &telebot.Chat{ID: *id}, Sender: &telebot.User{ID: 42, FirstName: "Fixture"}}
			got := askChatGptWithMode(msg, false, nil, false)
			wantCalls := 2
			if afterRetry {
				wantCalls = 3
			}
			if got != "fixture continued" || calls != wantCalls {
				t.Fatalf("reply=%q calls=%d want=%d", got, calls, wantCalls)
			}
		})
	}
}
