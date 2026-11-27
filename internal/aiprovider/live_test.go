package aiprovider_test

import (
	"context"
	"github.com/focusshifter/muxgoob/internal/aiprovider"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"os"
	"testing"
	"time"
)

// Unpublished, paid, opt-in probe only. Never runs from ordinary go test.
func TestOrcaRouterLiveSmoke(t *testing.T) {
	if os.Getenv("ORCAROUTER_LIVE_TEST") != "1" {
		t.Skip("set ORCAROUTER_LIVE_TEST=1 and ORCAROUTER_API_KEY to run paid smoke")
	}
	key := os.Getenv("ORCAROUTER_API_KEY")
	if key == "" {
		t.Fatal("ORCAROUTER_API_KEY is required")
	}
	old := registry.Config
	t.Cleanup(func() { registry.Config = old })
	registry.Config.OrcarouterApiKey = key
	model := os.Getenv("ORCAROUTER_TEST_MODEL")
	if model == "" {
		model = "orcarouter/auto"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := aiprovider.Router(aiprovider.OrcaRouter)
	req := openai.ChatCompletionRequest{Model: model, MaxTokens: 64, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "Call smoke_echo exactly once with an empty object. This is an unpublished connectivity test."}}, Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "smoke_echo", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}}}, ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "smoke_echo"}}}
	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("expected one tool call")
	}
	msg := resp.Choices[0].Message
	call := msg.ToolCalls[0]
	if call.Function.Name != "smoke_echo" {
		t.Fatal("unexpected tool")
	}
	req.Messages = append(req.Messages, msg, openai.ChatCompletionMessage{Role: "tool", ToolCallID: call.ID, Content: "ok"})
	req.ToolChoice = "none"
	resp, err = client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		t.Fatal("empty continuation")
	}
}
