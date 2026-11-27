package orcarouter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestResponsesToolChoiceMapping(t *testing.T) {
	for _, choice := range []any{"auto", "none", "required", openai.ToolChoice{Type: "function", Function: openai.ToolFunction{Name: "lookup"}}, map[string]any{"type": "function", "name": "lookup"}} {
		got, err := responsesToolChoice(choice)
		if err != nil {
			t.Fatal(err)
		}
		if m, ok := got.(map[string]any); ok {
			if len(m) != 2 || m["name"] != "lookup" || m["type"] != "function" {
				t.Error(m)
			}
		} else if got != choice {
			t.Errorf("%v != %v", got, choice)
		}
	}
	for _, choice := range []any{"invalid", map[string]any{"type": "function"}, map[string]any{"type": "web_search"}, func() {}} {
		if _, err := responsesToolChoice(choice); err == nil {
			t.Error("accepted invalid choice")
		}
	}
}
func TestResponsesMultipartAssistantAndRequestValidation(t *testing.T) {
	req := openai.ChatCompletionRequest{Model: "grok/grok-4.6", Messages: []openai.ChatCompletionMessage{{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: "text", Text: "look"}, {Type: "image_url", ImageURL: &openai.ChatMessageImageURL{URL: "https://example.com/photo.png"}}}}, {Role: "assistant", MultiContent: []openai.ChatMessagePart{{Type: "text", Text: "seen"}}}, {Role: "tool", ToolCallID: "call_empty", Content: ""}}}
	body, err := responsesRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	in := body["input"].([]any)
	user := in[0].(map[string]any)["content"].([]any)
	assistant := in[1].(map[string]any)["content"].([]any)
	if len(user) != 2 || assistant[0].(map[string]any)["type"] != "output_text" || in[2].(map[string]any)["output"] != "" {
		t.Error(in)
	}
	for _, m := range []openai.ChatCompletionMessage{{Role: "unknown", Content: "x"}, {Role: "tool", Content: "x"}, {Role: "user", Content: "x", MultiContent: []openai.ChatMessagePart{{Type: "text", Text: "y"}}}, {Role: "user", MultiContent: []openai.ChatMessagePart{{Type: "image_url"}}}, {Role: "user", MultiContent: []openai.ChatMessagePart{{Type: "audio"}}}, {Role: "function", Content: "x"}, {Role: "user", ToolCalls: []openai.ToolCall{{ID: "x", Type: "function", Function: openai.FunctionCall{Name: "lookup"}}}}} {
		req.Messages = []openai.ChatCompletionMessage{m}
		if _, err = responsesRequest(req); err == nil {
			t.Errorf("accepted unsupported history %+v", m)
		}
	}
}
func TestIncompleteFunctionNeverExecutable(t *testing.T) {
	for _, status := range []string{"incomplete", "in_progress"} {
		// Decode the actual wire item, not a hand-built function struct.
		c, _ := server(t, func(*http.Request, map[string]any) {}, `{"status":"completed","output":[{"type":"function_call","status":"`+status+`","call_id":"c","name":"lookup","arguments":"{}"}]}`)
		_, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "grok/grok-4.6:online"})
		if err == nil || !strings.Contains(err.Error(), "incomplete function") {
			t.Fatal(err)
		}
	}
}
func TestCanceledTransportAndRedirectSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewClient("private", WithBaseURL("http://127.0.0.1:1"))
	_, err := c.CreateChatCompletion(ctx, openai.ChatCompletionRequest{Model: "orcarouter/auto"})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed key-bearing redirect") }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c = NewClient("private", WithBaseURL(source.URL), WithHTTPClient(source.Client()))
	_, err = c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "orcarouter/auto"})
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatal(err)
	}
}
