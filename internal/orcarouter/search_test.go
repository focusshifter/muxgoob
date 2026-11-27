package orcarouter

import (
	"context"
	"encoding/json"
	"fmt"
	openai "github.com/sashabaranov/go-openai"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func genericRequest() openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{Model: "deepseek/deepseek-chat:online", Tools: []openai.Tool{hostTool()}, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "Search OrcaRouter web search documentation DeepSeek"}, image()}}
}
func toolResult(id, content string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: "tool", ToolCallID: id, Content: content}
}

const searchInspect = `{"success":true,"data":{"id":"branddev.web-search","price":{"mode":"per_call","for_sale":true,"sell_usd":0.00242},"health":{"ok":true},"input_schema":{"type":"object","required":["numResults","query"],"properties":{"query":{"type":"string"},"numResults":{"type":"integer","enum":[10]}}}}}`

// Captured shape from the authenticated real run, with account values replaced
// by sentinels. Exa in the docs is an example; our only paid ID is branddev.
const searchRun = `{"success":true,"data":{"run_id":"run_fixture","status":"completed","billed_usd":0.00242,"output":{"query":"OrcaRouter web search documentation DeepSeek","key_metadata":{"credits_remaining":"ACCOUNT_SENTINEL"},"cache_metadata":{"status":"CACHE_SENTINEL"},"results":[{"title":"Web search","url":"https://docs.orcarouter.ai/advanced/web-search","description":"DeepSeek API does not expose web search.","highlights":{"code":"NOT_REQUESTED"}}]}}}`

func searchReply(calls ...string) string {
	return `{"id":"chat_search","choices":[{"message":{"role":"assistant","tool_calls":[` + strings.Join(calls, ",") + `]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
}
func searchCall(id string) string {
	return fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":"orcarouter_webSearch","arguments":"{\"query\":\"OrcaRouter web search documentation DeepSeek\"}"}}`, id)
}
func hostCall() string {
	return `{"id":"call_host","type":"function","function":{"name":"lookup","arguments":"{}"}}`
}

func TestGenericSearchInternalAndMixedHostContinuation(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			chats, runs, inspects := 0, 0, 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer own-key" {
					t.Error("wrong credential")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/tools/branddev.web-search":
					if r.Method != "GET" {
						t.Error(r.Method)
					}
					inspects++
					fmt.Fprint(w, searchInspect)
				case "/v1/tools/branddev.web-search/run":
					runs++
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					in := body["input"].(map[string]any)
					if len(body) != 1 || len(in) != 2 || in["numResults"] != float64(10) || in["query"] != "OrcaRouter web search documentation DeepSeek" {
						t.Error(body)
					}
					fmt.Fprint(w, searchRun)
				case "/v1/chat/completions":
					chats++
					var b map[string]any
					_ = json.NewDecoder(r.Body).Decode(&b)
					wire, _ := json.Marshal(b)
					if b["model"] != "deepseek/deepseek-chat" {
						t.Error(b["model"])
					}
					tools := b["tools"].([]any)
					if len(tools) != 2 {
						t.Fatal(tools)
					}
					if chats == 1 {
						if mixed {
							fmt.Fprint(w, searchReply(searchCall("s1"), hostCall()))
						} else {
							fmt.Fprint(w, searchReply(searchCall("s1")))
						}
						return
					}
					// The next request must retain URL/snippet evidence but not account fields.
					for _, bad := range []string{"ACCOUNT_SENTINEL", "CACHE_SENTINEL", "credits_remaining", "key_metadata", "highlights"} {
						if strings.Contains(string(wire), bad) {
							t.Errorf("metadata leaked: %s", bad)
						}
					}
					if !strings.Contains(string(wire), "https://docs.orcarouter.ai/advanced/web-search") || !strings.Contains(string(wire), "DeepSeek API does not expose web search.") {
						t.Error("lost evidence")
					}
					messages := b["messages"].([]any)
					foundResult := false
					for _, raw := range messages {
						m := raw.(map[string]any)
						if m["role"] == "tool" {
							if mixed && m["tool_call_id"] != "call_host" {
								t.Error("orphan internal tool history")
							}
							foundResult = true
						}
					}
					if !foundResult {
						t.Error("lost tool result")
					}
					fmt.Fprint(w, chatCompleted)
				default:
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			c := NewClient("own-key", WithBaseURL(s.URL+"/v1"), WithHTTPClient(s.Client()))
			req := genericRequest()
			out, err := c.CreateChatCompletion(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if mixed {
				m := out.Choices[0].Message
				if len(m.ToolCalls) != 1 || m.ToolCalls[0].Function.Name != "lookup" || !strings.Contains(m.Content, "Untrusted web search evidence") {
					t.Fatalf("bad bubble: %+v", m)
				}
				req.Messages = append(req.Messages, m, toolResult("call_host", "host result"))
				out, err = c.CreateChatCompletion(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
			}
			if out.Choices[0].Message.Content != "ok" || runs != 1 || chats != 2 {
				t.Fatalf("out=%+v runs=%d chats=%d", out, runs, chats)
			}
			if !mixed && out.Usage.TotalTokens != 5 {
				t.Error(out.Usage)
			}
			wantInspect := 1
			if mixed {
				wantInspect = 2
			}
			if inspects != wantInspect {
				t.Error(inspects)
			}
		})
	}
}
func TestGenericSearchBudgetFailureAndIsolation(t *testing.T) {
	for _, scenario := range []string{"budget", "overpriced", "schema", "unhealthy", "run_failed", "empty", "http_error", "invalid_args", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			chats, runs := 0, 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/tools/branddev.web-search":
					body := searchInspect
					switch scenario {
					case "overpriced":
						body = strings.Replace(body, "0.00242", "0.1", 1)
					case "schema":
						body = strings.Replace(body, "[10]", "[5]", 1)
					case "unhealthy":
						body = strings.Replace(body, `"ok":true`, `"ok":false`, 1)
					}
					fmt.Fprint(w, body)
				case "/tools/branddev.web-search/run":
					runs++
					switch scenario {
					case "run_failed":
						fmt.Fprint(w, `{"success":false,"data":{"status":"failed"}}`)
					case "empty":
						fmt.Fprint(w, `{"success":true,"data":{"status":"completed","output":{"results":[]}}}`)
					case "http_error":
						w.WriteHeader(402)
						fmt.Fprint(w, `{"error":"PRIVATE"}`)
					default:
						fmt.Fprint(w, searchRun)
					}
				case "/chat/completions":
					chats++
					if scenario == "invalid_args" {
						fmt.Fprint(w, strings.Replace(searchReply(searchCall("s1")), `OrcaRouter web search documentation DeepSeek`, ``, 1))
					} else {
						fmt.Fprint(w, searchReply(searchCall(fmt.Sprintf("s%d", chats))))
					}
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer s.Close()
			c := NewClient("own-key", WithBaseURL(s.URL), WithHTTPClient(s.Client()))
			req := genericRequest()
			if scenario == "conflict" {
				req.Tools = append(req.Tools, searchTool())
			}
			_, err := c.CreateChatCompletion(context.Background(), req)
			if err == nil {
				t.Fatal("expected explicit failure, not offline fallback")
			}
			if scenario == "budget" && (runs != 2 || chats != 3) {
				t.Errorf("budget: runs=%d chats=%d", runs, chats)
			}
			if scenario == "conflict" && chats != 0 {
				t.Error("network before validation")
			}
			if (scenario == "overpriced" || scenario == "schema" || scenario == "unhealthy" || scenario == "invalid_args") && runs != 0 {
				t.Error("spent despite failed validation")
			}
			if (scenario == "run_failed" || scenario == "empty" || scenario == "http_error") && runs != 1 {
				t.Error("retried paid run")
			}
		})
	}
}
