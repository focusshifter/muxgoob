package openaicodex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

type capturedRequest struct {
	Model        string            `json:"model"`
	Instructions string            `json:"instructions"`
	Store        bool              `json:"store"`
	Stream       bool              `json:"stream"`
	Input        []json.RawMessage `json:"input"`
	Tools        []map[string]any  `json:"tools"`
}

func toolStringField(tool map[string]any, key string) string {
	value, _ := tool[key].(string)
	return value
}

func writeAuthFile(t *testing.T, dir string, token string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	payload := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  token,
			"refresh_token": "refresh-token",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), body, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
}

func TestClientCreateChatCompletionReturnsAssistantMessage(t *testing.T) {
	var got capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-access-token" {
			t.Fatalf("unexpected auth header: %q", auth)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\",\"created_at\":123,\"model\":\"gpt-5.4\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.output_item.done\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello from codex\"}]},\"output_index\":0,\"sequence_number\":1}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_123\",\"created_at\":123,\"model\":\"gpt-5.4\",\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18}}}\n\n")
	}))
	defer server.Close()

	codexHome := t.TempDir()
	writeAuthFile(t, codexHome, "test-access-token")

	client := NewClient(WithBaseURL(server.URL), WithCodexHome(codexHome))
	resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "system rules"},
			{Role: openai.ChatMessageRoleUser, Content: "say hi"},
		},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion error: %v", err)
	}
	if got.Model != "gpt-5.4" {
		t.Fatalf("expected model gpt-5.4, got %q", got.Model)
	}
	if got.Instructions != "system rules" {
		t.Fatalf("expected instructions to use system prompt, got %q", got.Instructions)
	}
	if got.Store {
		t.Fatalf("expected store=false")
	}
	if !got.Stream {
		t.Fatalf("expected stream=true")
	}
	if len(got.Input) != 1 {
		t.Fatalf("expected 1 input item, got %d", len(got.Input))
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != openai.FinishReasonStop {
		t.Fatalf("expected finish reason stop, got %q", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.Content != "hello from codex" {
		t.Fatalf("expected assistant content, got %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 18 {
		t.Fatalf("expected usage total 18, got %d", resp.Usage.TotalTokens)
	}
}

func TestNormalizeConfiguredModel(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		wantModel        string
		wantNativeSearch bool
		wantCodex        bool
	}{
		{name: "plain gpt name stays codex compatible", input: "gpt-5.4", wantModel: "gpt-5.4", wantNativeSearch: false, wantCodex: true},
		{name: "openai prefix is stripped", input: "openai/gpt-5.4", wantModel: "gpt-5.4", wantNativeSearch: false, wantCodex: true},
		{name: "online suffix enables native search", input: "openai/gpt-5.4:online", wantModel: "gpt-5.4", wantNativeSearch: true, wantCodex: true},
		{name: "non-openai routed away from codex", input: "google/gemini-2.5-flash", wantModel: "google/gemini-2.5-flash", wantNativeSearch: false, wantCodex: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeConfiguredModel(tc.input)
			if got.Model != tc.wantModel || got.NativeWebSearch != tc.wantNativeSearch || got.UseCodex != tc.wantCodex {
				t.Fatalf("NormalizeConfiguredModel(%q) = %+v, want model=%q native=%v useCodex=%v", tc.input, got, tc.wantModel, tc.wantNativeSearch, tc.wantCodex)
			}
		})
	}
}

func TestClientCreateChatCompletionAddsNativeWebSearchForOnlineModel(t *testing.T) {
	var got capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_online\",\"created_at\":123,\"model\":\"gpt-5.4\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.output_item.done\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"looked it up\"}]},\"output_index\":0,\"sequence_number\":1}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_online\",\"created_at\":123,\"model\":\"gpt-5.4\"}}\n\n")
	}))
	defer server.Close()

	codexHome := t.TempDir()
	writeAuthFile(t, codexHome, "test-access-token")

	client := NewClient(WithBaseURL(server.URL), WithCodexHome(codexHome))
	_, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "openai/gpt-5.4:online",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "Use tools when needed."},
			{Role: openai.ChatMessageRoleUser, Content: "Search the web and answer."},
		},
		Tools: []openai.Tool{
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "searchMessages", Description: "search", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion error: %v", err)
	}
	if got.Model != "gpt-5.4" {
		t.Fatalf("expected normalized model gpt-5.4, got %q", got.Model)
	}
	if len(got.Tools) != 2 {
		t.Fatalf("expected 2 tools including native web_search, got %d", len(got.Tools))
	}
	if toolStringField(got.Tools[0], "name") != "searchMessages" {
		t.Fatalf("expected first tool to remain searchMessages, got %#v", got.Tools[0])
	}
	if toolStringField(got.Tools[1], "type") != "web_search" {
		t.Fatalf("expected second tool to be native web_search, got %#v", got.Tools[1])
	}
	if value, ok := got.Tools[1]["external_web_access"].(bool); !ok || !value {
		t.Fatalf("expected native web_search external_web_access=true, got %#v", got.Tools[1]["external_web_access"])
	}
}

func TestClientCreateChatCompletionReturnsToolCallsAndForcedToolChoiceInstruction(t *testing.T) {
	var got capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_tools\",\"created_at\":123,\"model\":\"gpt-5.4\"}}\n\n")
		_, _ = io.WriteString(w, "event: response.output_item.done\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"status\":\"completed\",\"call_id\":\"call_123\",\"name\":\"searchMessages\",\"arguments\":\"{\\\"query\\\":\\\"pizza\\\"}\"},\"output_index\":0,\"sequence_number\":1}\n\n")
		_, _ = io.WriteString(w, "event: response.completed\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_tools\",\"created_at\":123,\"model\":\"gpt-5.4\"}}\n\n")
	}))
	defer server.Close()

	codexHome := t.TempDir()
	writeAuthFile(t, codexHome, "test-access-token")

	client := NewClient(WithBaseURL(server.URL), WithCodexHome(codexHome))
	resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: "gpt-5.4",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "Use tools when needed."},
			{Role: openai.ChatMessageRoleUser, Content: "Find pizza mentions."},
		},
		Tools: []openai.Tool{
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "searchMessages", Description: "search", Parameters: json.RawMessage(`{"type":"object"}`)}},
			{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "fetchUsers", Description: "fetch", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
		ToolChoice: openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "searchMessages"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion error: %v", err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("expected forced tool choice to narrow tools to 1, got %d", len(got.Tools))
	}
	if toolStringField(got.Tools[0], "name") != "searchMessages" {
		t.Fatalf("expected only searchMessages tool, got %q", toolStringField(got.Tools[0], "name"))
	}
	if !strings.Contains(got.Instructions, "You must call the searchMessages tool before responding.") {
		t.Fatalf("expected instructions to force searchMessages tool, got %q", got.Instructions)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != openai.FinishReasonToolCalls {
		t.Fatalf("expected finish reason tool_calls, got %q", resp.Choices[0].FinishReason)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Choices[0].Message.ToolCalls))
	}
	toolCall := resp.Choices[0].Message.ToolCalls[0]
	if toolCall.Function.Name != "searchMessages" {
		t.Fatalf("expected tool name searchMessages, got %q", toolCall.Function.Name)
	}
	if toolCall.Function.Arguments != `{"query":"pizza"}` {
		t.Fatalf("unexpected tool arguments: %s", toolCall.Function.Arguments)
	}
}
