package allmodels

import (
	"context"
	"encoding/json"
	openai "github.com/sashabaranov/go-openai"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWireOnlineToolsVisionAndContinuation(t *testing.T) {
	for _, online := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "online"}[online], func(t *testing.T) {
			model := "deepseek/deepseek-v4.1-flash-abliterated"
			configured := model
			if online {
				configured += ":online"
			}
			calls := 0
			client := NewClient("allmodels-only", WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://api.allmodels.io/oai/chat/completions" || r.Header.Get("Authorization") != "Bearer allmodels-only" {
					t.Fatal("wrong endpoint or auth")
				}
				if _, ok := r.Context().Deadline(); !ok {
					t.Fatal("unbounded request")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != model {
					t.Fatalf("model=%v", body["model"])
				}
				if body["provider"].(map[string]any)["zdr"] != true {
					t.Fatal("missing ZDR constraint")
				}
				search, exists := body["web_search_options"]
				if exists != online {
					t.Fatalf("search=%v", search)
				}
				if online && search.(map[string]any)["search_context_size"] != "medium" {
					t.Fatal("wrong search")
				}
				if len(body["tools"].([]any)) != 1 {
					t.Fatal("lost or added tools")
				}
				msgs := body["messages"].([]any)
				if len(msgs[0].(map[string]any)["content"].([]any)) != 2 {
					t.Fatal("lost vision")
				}
				out := `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"local-1","type":"function","function":{"name":"fixture","arguments":"{}"}}]}}]}`
				if calls == 2 {
					last := msgs[len(msgs)-1].(map[string]any)
					if last["tool_call_id"] != "local-1" || last["content"] != "local-result" {
						t.Fatal("lost tool continuation")
					}
					out = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"done","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.org/evidence","title":"Evidence"}}]}}]}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(out))}, nil
			})}))
			req := openai.ChatCompletionRequest{Model: configured, Messages: []openai.ChatCompletionMessage{{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "inspect"}, {Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,abc"}}}}}, Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "fixture"}}}}
			first, err := client.CreateChatCompletion(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			req.Messages = append(req.Messages, first.Choices[0].Message, openai.ChatCompletionMessage{Role: "tool", ToolCallID: "local-1", Content: "local-result"})
			final, err := client.CreateChatCompletion(context.Background(), req)
			if err != nil || final.Choices[0].Message.Content != "done" {
				t.Fatalf("continuation: %v", err)
			}
			if req.Model != configured || len(req.Tools) != 1 {
				t.Fatal("mutated caller")
			}
			if calls != 2 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestMissingKeyBeforeIOAndMalformedOnline(t *testing.T) {
	calls := 0
	option := WithHTTPClient(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { calls++; t.Fatal("unexpected IO"); return nil, nil })})
	for _, model := range []string{"native/auto", "vendor/model:online"} {
		if _, err := NewClient(" ", option).CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err == nil {
			t.Fatal("missing key accepted")
		}
	}
	for _, model := range []string{":online", "online:auto"} {
		if _, err := NewClient("test", option).CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err == nil {
			t.Fatal("empty/auto model accepted")
		}
	}
	if calls != 0 {
		t.Fatal("IO on invalid request")
	}
}

func TestOpaqueNativeIDsAndBaseURL(t *testing.T) {
	for _, model := range []string{"allmodels/auto", "vendor/native:variant"} {
		c := NewClient("test", WithBaseURL("https://fixture.invalid/custom/oai/"), WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/custom/oai/chat/completions" {
				t.Fatal(r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != model {
				t.Fatal(body["model"])
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"fixture"}}]}`))}, nil
		})}))
		if _, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
			t.Fatal(err)
		}
	}
}
