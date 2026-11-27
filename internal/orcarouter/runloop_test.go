package orcarouter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/focusshifter/muxgoob/internal/orcarouter"
	"github.com/focusshifter/muxgoob/internal/tools"
	openai "github.com/sashabaranov/go-openai"
)

type loopTool struct{ calls int }

func (t *loopTool) Definition() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "lookup", Parameters: map[string]any{"type": "object"}}}
}
func (t *loopTool) Execute(context.Context, string) (string, error) {
	t.calls++
	return fmt.Sprintf("result-%d", t.calls), nil
}

func TestResponsesRealRunLoopMultipleCalls(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/responses" {
			t.Error(r.URL.Path)
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if requests == 1 {
			fmt.Fprint(w, `{"id":"r1","status":"completed","output":[{"type":"web_search_call","status":"completed","id":"ws1"},{"type":"function_call","id":"fc1","call_id":"c1","name":"lookup","arguments":"{}"},{"type":"function_call","id":"fc2","call_id":"c2","name":"lookup","arguments":"{}"}]}`)
			return
		}
		in := b["input"].([]any)
		if len(in) != 5 {
			t.Fatalf("full history lost: %+v", in)
		}
		for i, id := range []string{"c1", "c2"} {
			call := in[1+i].(map[string]any)
			result := in[3+i].(map[string]any)
			if call["type"] != "function_call" || call["call_id"] != id || result["type"] != "function_call_output" || result["call_id"] != id || result["output"] != fmt.Sprintf("result-%d", i+1) {
				t.Error(in)
			}
		}
		fmt.Fprint(w, `{"id":"r2","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"verified continuation"}]}]}`)
	}))
	defer s.Close()
	tool := &loopTool{}
	registry := tools.NewRegistry(tool)
	client := orcarouter.NewClient("test-key", orcarouter.WithBaseURL(s.URL+"/v1"), orcarouter.WithHTTPClient(s.Client()))
	out, err := tools.RunLoop(context.Background(), client, openai.ChatCompletionRequest{Model: "grok/grok-4.6:online", Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "search and lookup twice"}}, Tools: registry.Definitions()}, registry, 3)
	if err != nil || out != "verified continuation" || tool.calls != 2 || requests != 2 {
		t.Fatalf("out=%s err=%v calls=%d requests=%d", out, err, tool.calls, requests)
	}
}
