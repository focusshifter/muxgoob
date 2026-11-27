package reply

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/internal/allmodels"
	openai "github.com/sashabaranov/go-openai"
)

// Explicit paid, unpublished, inert tool smoke against the incident upstream.
// No production DB/history, Telegram, tool side effects or review publication.
func TestAllModelsLiveStopToolCompatibility(t *testing.T) {
	if os.Getenv("ALLMODELS_LIVE_TEST") != "1" {
		t.Skip("requires explicit live opt-in")
	}
	key := os.Getenv("ALLMODELS_API_KEY")
	if strings.TrimSpace(key) == "" {
		t.Fatal("missing AllModels key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	base := http.DefaultTransport
	calls := 0
	firstReason := ""
	client := allmodels.NewClient(key, allmodels.WithHTTPClient(&http.Client{Timeout: 90 * time.Second, Transport: allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			t.Fatal("more than two paid POSTs")
		}
		if r.URL.Host != "api.allmodels.io" || r.URL.Path != "/oai/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Fatal("request escaped route/auth boundary")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal("request decode failed")
		}
		p := body["provider"].(map[string]any)
		if p["zdr"] != true {
			t.Fatal("missing ZDR")
		}
		p["only"] = []string{"refuseless"}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal("request encoding failed")
		}
		clone := r.Clone(r.Context())
		clone.Body = io.NopCloser(bytes.NewReader(encoded))
		clone.ContentLength = int64(len(encoded))
		clone.GetBody = nil
		resp, err := base.RoundTrip(clone)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var wire struct {
			Provider string `json:"provider"`
			Choices  []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &wire) != nil {
			t.Fatal("response decode failed")
		}
		if !strings.EqualFold(wire.Provider, "refuseless") {
			t.Fatal("requested upstream not verified")
		}
		if calls == 1 && len(wire.Choices) == 1 {
			firstReason = wire.Choices[0].FinishReason
		}
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return resp, nil
	})}))
	req := openai.ChatCompletionRequest{Model: "deepseek/deepseek-v4.1-flash-abliterated", MaxTokens: 2048, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "Call fixture_marker exactly once with an empty object. Then return only its returned marker."}}, Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "fixture_marker", Description: "Read a fixed harmless verification marker", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}}, ToolChoice: "auto"}
	first, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("first completion: %v", err)
	}
	if len(first.Choices) != 1 || first.Choices[0].FinishReason != openai.FinishReasonToolCalls || len(first.Choices[0].Message.ToolCalls) != 1 {
		t.Fatal("host tool not returned as executable tool_calls")
	}
	call := first.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "fixture_marker" {
		t.Fatal("unexpected host function")
	}
	req.Messages = append(req.Messages, first.Choices[0].Message, openai.ChatCompletionMessage{Role: "tool", ToolCallID: call.ID, Content: "STOP_TOOL_COMPAT_OK"})
	req.ToolChoice = "none"
	final, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("continuation: %v", err)
	}
	if len(final.Choices) != 1 || !strings.Contains(final.Choices[0].Message.Content, "STOP_TOOL_COMPAT_OK") {
		t.Fatal("tool-result continuation not verified")
	}
	if firstReason != "stop" && firstReason != "tool_calls" {
		t.Fatal("unexpected wire finish reason")
	}
	t.Logf("neutral real upstream=refuseless wire_reason=%s normalized_reason=tool_calls continuation=verified requests=%d zdr=true", firstReason, calls)
}
