package orcarouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// These tests never read config files, publish messages, or execute host tools.
// Paid network traffic requires BOTH explicit opt-in and an environment key.
// The only "host" tool below returns a constant in this test process.
func TestOnlineLiveGrokSearchAndHostContinuation(t *testing.T) {
	if os.Getenv("ORCAROUTER_ONLINE_LIVE") != "1" {
		t.Skip("set ORCAROUTER_ONLINE_LIVE=1 and ORCAROUTER_API_KEY to opt into paid native smoke")
	}
	model := os.Getenv("ORCAROUTER_TEST_MODEL")
	if model == "" {
		model = "grok/grok-4.6:online"
	}
	if onlineFamily(strings.TrimSuffix(model, ":online")) != "responses" || !strings.HasSuffix(model, ":online") {
		t.Fatal("native smoke requires explicit Responses-capable :online model")
	}
	runOnlineLive(t, model, false)
}
func TestOnlineLiveGenericSearchAndHostContinuation(t *testing.T) {
	if os.Getenv("ORCAROUTER_GENERIC_ONLINE_LIVE") != "1" {
		t.Skip("set ORCAROUTER_GENERIC_ONLINE_LIVE=1 and ORCAROUTER_API_KEY to opt into paid generic smoke")
	}
	model := os.Getenv("ORCAROUTER_GENERIC_TEST_MODEL")
	if model == "" {
		model = "deepseek/deepseek-chat:online"
	}
	if onlineFamily(strings.TrimSuffix(model, ":online")) != "generic" || !strings.HasSuffix(model, ":online") {
		t.Fatal("generic smoke requires explicit nonnative :online model")
	}
	runOnlineLive(t, model, true)
}
func runOnlineLive(t *testing.T, model string, generic bool) {
	t.Helper()
	key := os.Getenv("ORCAROUTER_API_KEY")
	if strings.TrimSpace(key) == "" {
		t.Fatal("ORCAROUTER_API_KEY required when live smoke enabled")
	}
	audit := &liveAudit{base: http.DefaultTransport, maxRequests: 12}
	defer func() {
		t.Logf("audit model=%s response_ids=%v builtin_search_calls=%d completed_search_runs=%d run_ids=%v billed_usd=%v", model, audit.responseIDs, audit.webSearchCalls, audit.searchRuns, audit.runIDs, audit.costs)
	}()
	c := NewClient(key, WithHTTPClient(&http.Client{Transport: audit, Timeout: 90 * time.Second}))
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	req := openai.ChatCompletionRequest{Model: model, MaxCompletionTokens: 2000, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "Search the web for OrcaRouter web search API documentation. Summarize one result briefly and cite its exact source URL. You must actually search, not answer from memory."}}, ToolChoice: "required"}
	if generic {
		req.ToolChoice = openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: reservedSearch}}
	}
	first, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("search phase: %v", err)
	}
	if len(first.Choices) != 1 || len(first.Choices[0].Message.ToolCalls) != 0 || first.Choices[0].Message.Content == "" {
		t.Fatal("search phase did not produce final text")
	}
	if generic {
		if audit.searchRuns == 0 {
			t.Fatal("no completed standalone search evidence")
		}
	} else {
		if audit.webSearchCalls == 0 {
			t.Fatal("no actual completed builtin web_search_call evidence")
		}
	}
	req.Messages = append(req.Messages, first.Choices[0].Message, openai.ChatCompletionMessage{Role: "user", Content: "Now call the lookup host function once with an empty JSON object, then wait for its result."})
	req.Tools = []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "lookup", Description: "A harmless in-process smoke test returns a fixed verification token.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}}
	req.ToolChoice = openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "lookup"}}
	second, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("host phase: %v", err)
	}
	if len(second.Choices) != 1 || len(second.Choices[0].Message.ToolCalls) != 1 || second.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
		t.Fatal("host function not returned exactly once")
	}
	call := second.Choices[0].Message.ToolCalls[0]
	req.Messages = append(req.Messages, second.Choices[0].Message, openai.ChatCompletionMessage{Role: "tool", ToolCallID: call.ID, Content: `{"verification":"orca-smoke-confirmed-7319"}`}, openai.ChatCompletionMessage{Role: "user", Content: "State the exact verification token from the host tool result, and no other text. Do not search or call any tools."})
	req.ToolChoice = "none"
	third, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("continuation phase: %v", err)
	}
	if len(third.Choices) != 1 || !strings.Contains(third.Choices[0].Message.Content, "orca-smoke-confirmed-7319") || len(third.Choices[0].Message.ToolCalls) != 0 {
		t.Fatal("continuation did not consume host result")
	}
	t.Logf("verified model=%s response_ids=%v builtin_search_calls=%d completed_search_runs=%d run_ids=%v billed_usd=%v host_call_id=%s final_response_id=%s", model, audit.responseIDs, audit.webSearchCalls, audit.searchRuns, audit.runIDs, audit.costs, call.ID, third.ID)
}

// Keep only IDs, counts and billing metadata, never headers, queries, answer
// bodies, account metadata, or inline data URLs. Still pass exact bytes onward.
type liveAudit struct {
	mu                                                sync.Mutex
	base                                              http.RoundTripper
	maxRequests, requests, webSearchCalls, searchRuns int
	responseIDs, runIDs                               []string
	costs                                             []float64
}

func (a *liveAudit) RoundTrip(req *http.Request) (*http.Response, error) {
	a.mu.Lock()
	a.requests++
	over := a.requests > a.maxRequests
	a.mu.Unlock()
	if over {
		return nil, fmt.Errorf("live smoke HTTP request budget exceeded")
	}
	resp, err := a.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("live response size limit")
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	var raw struct {
		ID     string `json:"id"`
		Output []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"output"`
		Success bool `json:"success"`
		Data    struct {
			RunID  string  `json:"run_id"`
			Status string  `json:"status"`
			Cost   float64 `json:"billed_usd"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &raw) == nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		if raw.ID != "" {
			a.responseIDs = append(a.responseIDs, raw.ID)
		}
		for _, o := range raw.Output {
			if o.Type == "web_search_call" && o.Status == "completed" {
				a.webSearchCalls++
			}
		}
		if strings.HasSuffix(req.URL.Path, "/run") && raw.Success && raw.Data.Status == "completed" {
			a.searchRuns++
			a.runIDs = append(a.runIDs, raw.Data.RunID)
			a.costs = append(a.costs, raw.Data.Cost)
		}
	}
	return resp, nil
}
