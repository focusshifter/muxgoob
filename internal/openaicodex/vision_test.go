package openaicodex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestBuildRequestPreservesVisionParts(t *testing.T) {
	for _, tc := range []struct {
		name, url string
		detail    openai.ImageURLDetail
	}{
		{"data", "data:image/jpeg;base64,aGVsbG8=", openai.ImageURLDetailLow},
		{"remote", "https://example.com/image.png", openai.ImageURLDetailHigh},
		{"default", "https://example.com/image.png", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, imageOnly := range []bool{false, true} {
				parts := []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: tc.url, Detail: tc.detail}}}
				if !imageOnly {
					parts = append([]openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "before"}}, append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: "after"})...)
				}
				payload, err := NewClient().buildRequest(openai.ChatCompletionRequest{Model: "openai/gpt-6-luna", Messages: []openai.ChatCompletionMessage{{Role: "system", Content: "rules"}, {Role: "user", MultiContent: parts}}})
				if err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Instructions string `json:"instructions"`
					Input        []struct {
						Content []map[string]string `json:"content"`
					} `json:"input"`
				}
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				if got.Instructions != "rules" || len(got.Input) != 1 || len(got.Input[0].Content) != len(parts) {
					t.Fatalf("imageOnly=%v lost content: %s", imageOnly, raw)
				}
				i := 0
				if !imageOnly {
					i = 1
					if got.Input[0].Content[0]["text"] != "before" || got.Input[0].Content[2]["text"] != "after" {
						t.Fatalf("text order: %s", raw)
					}
				}
				image := got.Input[0].Content[i]
				detail := string(tc.detail)
				if detail == "" {
					detail = "auto"
				}
				if image["type"] != "input_image" || image["image_url"] != tc.url || image["detail"] != detail {
					t.Fatalf("wrong image: %s", raw)
				}
				if _, ok := image["text"]; ok {
					t.Fatalf("image in text: %s", raw)
				}
			}
		})
	}
}

func TestVisionFallbackPreservesImagesAndSafety(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "disabled"}[disabled], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			home := t.TempDir()
			writeAuthFile(t, home, "local-test-token")
			fallback := &fallbackClientStub{}
			client := NewClient(WithBaseURL(server.URL), WithCodexHome(home), WithFallbackClient(fallback))
			request := openai.ChatCompletionRequest{Model: "gpt-6-luna", Messages: []openai.ChatCompletionMessage{{Role: "user", MultiContent: []openai.ChatMessagePart{
				{Type: openai.ChatMessagePartTypeText, Text: "inspect"},
				{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/jpeg;base64,aGVsbG8=", Detail: openai.ImageURLDetailLow}},
			}}}}
			ctx := context.Background()
			if disabled {
				ctx = WithoutFallback(ctx)
			}
			_, err := client.CreateChatCompletion(ctx, request)
			if disabled {
				if err == nil || len(fallback.requests) != 0 {
					t.Fatalf("WithoutFallback bypassed: err=%v calls=%d", err, len(fallback.requests))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(fallback.requests) != 1 || fallback.requests[0].Model != "openai/gpt-6-luna" || !reflect.DeepEqual(fallback.requests[0].Messages, request.Messages) {
				t.Fatalf("fallback lost images or normalization: %+v", fallback.requests)
			}
		})
	}
}

func TestBuildRequestPreservesTextAndToolSemantics(t *testing.T) {
	request := openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{
		{Role: "system", Content: "rules"},
		{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: " before "}, {Type: openai.ChatMessagePartTypeText, Text: " after "}}},
		{Role: "assistant", Content: "thinking", ToolCalls: []openai.ToolCall{{ID: "call1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "check", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call1", Content: "result"},
	}}
	payload, err := NewClient().buildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Input) != 4 || payload.Instructions != "rules" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	user := payload.Input[0].(codexMessageInput)
	if len(user.Content) != 1 || user.Content[0].Text != "before \n after" {
		t.Fatalf("text joining changed: %+v", user)
	}
	if payload.Input[2].(codexFunctionCallInput).CallID != "call1" || payload.Input[3].(codexFunctionCallOutputInput).Output != "result" {
		t.Fatalf("tool history changed: %+v", payload.Input)
	}
}

func TestBuildRequestRejectsInvalidImage(t *testing.T) {
	for _, part := range []openai.ChatMessagePart{
		{Type: openai.ChatMessagePartTypeImageURL},
		{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{}},
		{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "https://example.com/image.png", Detail: "invalid"}},
	} {
		_, err := NewClient().buildRequest(openai.ChatCompletionRequest{Messages: []openai.ChatCompletionMessage{{Role: "user", MultiContent: []openai.ChatMessagePart{part}}}})
		if err == nil {
			t.Fatal("invalid image silently omitted")
		}
	}
}
