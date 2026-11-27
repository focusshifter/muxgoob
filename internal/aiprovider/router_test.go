package aiprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOrcaRouterWireAndMissingKeyIsolation(t *testing.T) {
	old := registry.Config
	defer func() { registry.Config = old }()
	registry.Config.OrcarouterApiKey = "orca-only"
	registry.Config.OpenrouterApiKey = "never-to-orca"
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	calls := 0
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.orcarouter.ai" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer orca-only" {
			t.Fatalf("wrong route/auth: %s %q", r.URL, r.Header.Get("Authorization"))
		}
		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "orcarouter/auto" || len(req.Tools) != 1 || len(req.Messages[0].MultiContent) != 1 {
			t.Fatalf("lost model/tools/image: %+v", req)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"fixture"}}]}`))}, nil
	})
	req := openai.ChatCompletionRequest{Model: "orcarouter/auto", Messages: []openai.ChatCompletionMessage{{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,abc"}}}}}, Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "test"}}}}
	resp, callErr := Router("orcarouter").CreateChatCompletion(context.Background(), req)
	if callErr != nil || len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "fixture" {
		t.Fatalf("completion=%+v err=%v", resp, callErr)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	registry.Config.OrcarouterApiKey = ""
	_, err := Router("orcarouter").CreateChatCompletion(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "orcarouter api key is not configured") || calls != 1 {
		t.Fatalf("missing key isolation: %v calls=%d", err, calls)
	}
}
