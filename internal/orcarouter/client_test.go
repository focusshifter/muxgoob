package orcarouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

const completed = `{"id":"resp_1","object":"response","model":"grok/grok-4.6","status":"completed","created_at":123,"output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"},{"type":"output_text","text":"two"}]}],"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`
const chatCompleted = `{"id":"chat_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

func hostTool() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "lookup", Description: "lookup", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}
}
func image() openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,PRIVATE", Detail: openai.ImageURLDetailLow}}}}
}
func server(t *testing.T, check func(*http.Request, map[string]any), response string) (*Client, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer only-orca" {
			t.Error("wrong own credential")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		check(r, body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	t.Cleanup(s.Close)
	return NewClient("only-orca", WithBaseURL(s.URL+"/v1"), WithHTTPClient(s.Client())), s
}
func TestNonOnlineOpaquePassthrough(t *testing.T) {
	for _, model := range []string{"orcarouter/auto", "orcarouter/free", "x-ai/grok-4.6", "deepseek/deepseek-chat"} {
		t.Run(model, func(t *testing.T) {
			req := openai.ChatCompletionRequest{Model: model, Messages: []openai.ChatCompletionMessage{image()}, Tools: []openai.Tool{hostTool()}, ToolChoice: openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "lookup"}}, Temperature: 0.3, MaxTokens: 200}
			expectedBytes, _ := json.Marshal(req)
			var expected map[string]any
			_ = json.Unmarshal(expectedBytes, &expected)
			c, _ := server(t, func(r *http.Request, b map[string]any) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Error(r.URL.Path)
				}
				if !reflect.DeepEqual(b, expected) {
					t.Errorf("passthrough changed: %v", b)
				}
			}, chatCompleted)
			out, err := c.CreateChatCompletion(context.Background(), req)
			if err != nil || out.Choices[0].Message.Content != "ok" {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
}
func TestOnlineFamilies(t *testing.T) {
	for _, tt := range []struct{ model, path string }{{"anthropic/claude-sonnet-4:online", "/v1/chat/completions"}, {"google/gemini-2.5-flash:online", "/v1/chat/completions"}, {"grok/grok-4.6:online", "/v1/responses"}, {"x-ai/grok-4.6:online", "/v1/responses"}, {"openai/gpt-5:online", "/v1/responses"}} {
		t.Run(tt.model, func(t *testing.T) {
			req := openai.ChatCompletionRequest{Model: tt.model, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "search"}}, Tools: []openai.Tool{hostTool()}}
			response := chatCompleted
			if tt.path == "/v1/responses" {
				response = completed
			}
			c, _ := server(t, func(r *http.Request, b map[string]any) {
				if r.URL.Path != tt.path || b["model"] != strings.TrimSuffix(tt.model, ":online") {
					t.Errorf("wrong route/model: %s %v", r.URL.Path, b["model"])
				}
				if strings.HasPrefix(tt.model, "anthropic/") {
					if !reflect.DeepEqual(b["web_search_options"], map[string]any{"search_context_size": "medium"}) {
						t.Error(b)
					}
				} else {
					tools := b["tools"].([]any)
					if len(tools) != 2 {
						t.Fatal(tools)
					}
					if strings.HasPrefix(tt.model, "google/") {
						if tools[1].(map[string]any)["function"].(map[string]any)["name"] != "googleSearch" {
							t.Error(tools)
						}
					} else {
						if tools[0].(map[string]any)["type"] != "web_search" {
							t.Error(tools)
						}
						if b["stream"] != false || b["store"] != false || b["messages"] != nil || b["previous_response_id"] != nil {
							t.Error(b)
						}
					}
				}
			}, response)
			_, err := c.CreateChatCompletion(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if req.Model != tt.model || len(req.Tools) != 1 {
				t.Fatal("mutated caller")
			}
		})
	}
}
func TestUnsupportedAndMissingKeyBeforeNetwork(t *testing.T) {
	c, _ := server(t, func(*http.Request, map[string]any) { t.Error("unexpected network") }, chatCompleted)
	for _, model := range []string{"orcarouter/auto:online", "unknown/foo:online", "online:auto", "google/not-gemini:online"} {
		_, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model})
		if err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("%s: %v", model, err)
		}
	}
	c.apiKey = " "
	_, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "grok/grok-4.6:online"})
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatal(err)
	}
}
func TestResponsesMultipleToolsContinuationAndImages(t *testing.T) {
	calls := 0
	response := `{"id":"resp_tools","model":"grok/grok-4.6","status":"completed","output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"function_call","id":"fc_a","call_id":"call_a","name":"lookup","arguments":"{\"n\":1}"},{"type":"function_call","id":"fc_b","call_id":"call_b","name":"lookup","arguments":"{\"n\":2}"}],"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`
	c, _ := server(t, func(r *http.Request, b map[string]any) {
		calls++
		in := b["input"].([]any)
		if calls == 1 {
			part := in[1].(map[string]any)["content"].([]any)[0].(map[string]any)
			if part["type"] != "input_image" || part["image_url"] != "data:image/png;base64,PRIVATE" || part["detail"] != "low" {
				t.Error(part)
			}
			if !reflect.DeepEqual(b["tool_choice"], map[string]any{"type": "function", "name": "lookup"}) {
				t.Error(b["tool_choice"])
			}
			if b["max_output_tokens"] != float64(400) || b["reasoning"].(map[string]any)["effort"] != "low" {
				t.Error(b)
			}
		} else {
			types := []string{}
			for _, item := range in {
				types = append(types, fmt.Sprint(item.(map[string]any)["type"]))
			}
			if strings.Join(types, ",") != "message,message,message,function_call,function_call,function_call_output,function_call_output,message,message" {
				t.Error(types)
			}
			for i, id := range []string{"call_a", "call_b"} {
				if in[3+i].(map[string]any)["call_id"] != id || in[5+i].(map[string]any)["call_id"] != id {
					t.Error(in)
				}
			}
			if in[5].(map[string]any)["output"] != "result A" || in[6].(map[string]any)["output"] != "result B" {
				t.Error("dropped tool results")
			}
		}
	}, response)
	req := openai.ChatCompletionRequest{Model: "grok/grok-4.6:online", Messages: []openai.ChatCompletionMessage{{Role: "system", Content: "system"}, image()}, Tools: []openai.Tool{hostTool()}, ToolChoice: openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "lookup"}}, MaxCompletionTokens: 400, ReasoningEffort: "low"}
	out, err := c.CreateChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "resp_tools" || out.Choices[0].FinishReason != "tool_calls" || len(out.Choices[0].Message.ToolCalls) != 2 || out.Usage.TotalTokens != 19 {
		t.Fatalf("%+v", out)
	}
	req.Messages = append(req.Messages, openai.ChatCompletionMessage{Role: "assistant", Content: "checking", ToolCalls: out.Choices[0].Message.ToolCalls}, openai.ChatCompletionMessage{Role: "tool", ToolCallID: "call_a", Content: "result A"}, openai.ChatCompletionMessage{Role: "tool", ToolCallID: "call_b", Content: "result B"}, openai.ChatCompletionMessage{Role: "user", Content: "continue"}, openai.ChatCompletionMessage{Role: "assistant", Content: "certainly"})
	req.ToolChoice = "auto"
	if _, err = c.CreateChatCompletion(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}
func TestResponsesStatusAndErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"completed", completed, ""},
		{"incomplete", `{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`, ""},
		{"failed", `{"id":"r","status":"failed","error":{"code":"provider_failed","message":"only-orca data:image/png;base64,PRIVATE"}}`, "failed"},
		{"pending", `{"status":"in_progress"}`, "in_progress"},
		{"empty", `{"status":"completed","output":[{"type":"web_search_call"}]}`, "empty"},
		{"malformed", `{broken`, "decode"},
		{"missing_status", `{"output":[]}`, "status"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := server(t, func(*http.Request, map[string]any) {}, tt.body)
			out, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "grok/grok-4.6:online"})
			if tt.want != "" {
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("%v", err)
				}
				if strings.Contains(err.Error(), "only-orca") || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal("secret leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.name == "completed" && (out.Choices[0].Message.Content != "onetwo" || out.Choices[0].FinishReason != "stop" || out.Created != 123) {
				t.Errorf("%+v", out)
			}
			if tt.name == "incomplete" && out.Choices[0].FinishReason != "length" {
				t.Error(out)
			}
		})
	}
}
func TestHTTPErrorSafe(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":{"message":"only-orca data:image/png;base64,PRIVATE"}}`)
	}))
	defer s.Close()
	c := NewClient("only-orca", WithBaseURL(s.URL))
	_, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "grok/grok-4.6:online"})
	if err == nil || !strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "only-orca") {
		t.Fatal(err)
	}
}
