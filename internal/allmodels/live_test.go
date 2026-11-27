package allmodels

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// Opt-in only: three bounded paid calls, no DB, Telegram, or review publishing.
// Inspect provider annotations on the wire; a model-written link is not proof.
func TestAllModelsLiveSmoke(t *testing.T) {
	if os.Getenv("ALLMODELS_LIVE_TEST") != "1" {
		t.Skip("requires explicit ALLMODELS_LIVE_TEST=1")
	}
	key := os.Getenv("ALLMODELS_API_KEY")
	if strings.TrimSpace(key) == "" {
		t.Fatal("ALLMODELS_API_KEY is required")
	}
	model := os.Getenv("ALLMODELS_TEST_MODEL")
	if model == "" {
		model = "deepseek/deepseek-v4.1-flash-abliterated"
	}
	if strings.HasPrefix(model, "allmodels:") || strings.HasSuffix(model, ":online") {
		t.Fatal("ALLMODELS_TEST_MODEL must be the exact plain upstream ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var latest map[string]any
	base := http.DefaultTransport
	client := NewClient(key, WithHTTPClient(&http.Client{Timeout: 90 * time.Second, Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.allmodels.io" || r.URL.Path != "/oai/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Fatal("live request escaped route/auth boundary")
		}
		resp, err := base.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		latest = nil
		_ = json.Unmarshal(data, &latest)
		resp.Body = io.NopCloser(bytes.NewReader(data))
		return resp, nil
	})}))
	req := openai.ChatCompletionRequest{Model: model, MaxTokens: 1024, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "Call fixture_marker exactly once, then return only its value."}}, Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "fixture_marker", Description: "Read an inert local verification marker", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}}}, ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "fixture_marker"}}}
	first, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Choices) != 1 || len(first.Choices[0].Message.ToolCalls) != 1 || first.Choices[0].Message.ToolCalls[0].Function.Name != "fixture_marker" {
		t.Fatal("live model did not issue the local tool")
	}
	call := first.Choices[0].Message.ToolCalls[0]
	req.Messages = append(req.Messages, first.Choices[0].Message, openai.ChatCompletionMessage{Role: "tool", ToolCallID: call.ID, Content: "gooby_allmodels_local_verified"})
	req.ToolChoice = "none"
	final, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Choices) != 1 || !strings.Contains(final.Choices[0].Message.Content, "gooby_allmodels_local_verified") {
		t.Fatal("live host-tool continuation not verified")
	}
	t.Log("host-tool invocation and continuation verified (unpublished)")
	_, err = client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{Model: model + ":online", MaxTokens: 2048, Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "Use hosted web search now to find the latest stable Go release on go.dev. Give the release version and date with cited sources."}}})
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := latest["provider"].(string)
	if provider == "" {
		t.Fatal("live response omitted serving provider; privacy proof incomplete")
	}
	choices, _ := latest["choices"].([]any)
	evidence := 0
	for _, choice := range choices {
		ch, _ := choice.(map[string]any)
		message, _ := ch["message"].(map[string]any)
		annotations, _ := message["annotations"].([]any)
		for _, annotation := range annotations {
			a, _ := annotation.(map[string]any)
			if a["type"] != "url_citation" {
				continue
			}
			citation, _ := a["url_citation"].(map[string]any)
			s, _ := citation["url"].(string)
			if s == "" {
				s, _ = a["url"].(string)
			}
			u, err := url.Parse(s)
			if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" {
				evidence++
			}
		}
	}
	if evidence == 0 {
		t.Fatal("hosted search NOT verified: no provider URL annotations (prose alone is insufficient)")
	}
	t.Logf("hosted search verified: %d provider URL annotations; ZDR requested on all calls", evidence)
}
