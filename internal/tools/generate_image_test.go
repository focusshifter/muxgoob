package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
)

type imageGeneratorStub struct {
	requests []openaicodex.ImageGenerationRequest
	resp     openaicodex.ImageGenerationResponse
	errs     []error
	err      error
}

func (s *imageGeneratorStub) GenerateImage(_ context.Context, request openaicodex.ImageGenerationRequest) (openaicodex.ImageGenerationResponse, error) {
	s.requests = append(s.requests, request)
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return s.resp, err
	}
	return s.resp, s.err
}

func TestGenerateImageToolExecuteGeneratesAndSendsImage(t *testing.T) {
	generatedPNG := []byte("fake-png")
	stub := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{
		Model:         "gpt-image-2",
		Image:         generatedPNG,
		MimeType:      "image/png",
		Extension:     "png",
		RevisedPrompt: "a revised prompt",
	}}
	var sentPath string
	var sentCaption string
	var actions []telebot.ChatAction
	tool := &GenerateImageTool{
		chatID:    123,
		generator: stub,
		outputDir: t.TempDir(),
		notify: func(chatID int64, action telebot.ChatAction) error {
			if chatID != 123 {
				t.Fatalf("expected notify chat id 123, got %d", chatID)
			}
			actions = append(actions, action)
			return nil
		},
		send: func(chatID int64, imagePath string, caption string) error {
			if chatID != 123 {
				t.Fatalf("expected chat id 123, got %d", chatID)
			}
			sentPath = imagePath
			sentCaption = caption
			return nil
		},
	}

	result, err := tool.Execute(context.Background(), `{"prompt":"нарисуй кота","caption":"цивик вышел подрифтить","size":"2048x1152","quality":"low","output_format":"png"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(stub.requests) != 1 {
		t.Fatalf("expected one generation request, got %d", len(stub.requests))
	}
	if stub.requests[0].Prompt != "нарисуй кота" || stub.requests[0].Model != "gpt-image-2" || stub.requests[0].Size != "1024x1024" {
		t.Fatalf("unexpected request: %#v", stub.requests[0])
	}
	if sentPath == "" {
		t.Fatal("expected image to be sent")
	}
	if len(actions) < 2 || actions[0] != telebot.Typing || actions[len(actions)-1] != telebot.UploadingPhoto {
		t.Fatalf("expected typing then uploading photo actions, got %#v", actions)
	}
	written, err := os.ReadFile(sentPath)
	if err != nil {
		t.Fatalf("read written image: %v", err)
	}
	if string(written) != "fake-png" {
		t.Fatalf("unexpected written image: %q", written)
	}
	if sentCaption != "цивик вышел подрифтить" {
		t.Fatalf("unexpected caption: %q", sentCaption)
	}
	if !tool.WasSent() {
		t.Fatal("expected tool to record successful send")
	}
	var payload generateImageResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !payload.Sent || payload.Model != "gpt-image-2" || payload.Size != "1024x1024" || payload.Path != sentPath {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestGenerateImageToolDoesNotUseRevisedPromptAsCaption(t *testing.T) {
	stub := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{
		Model:         "gpt-image-2",
		Image:         []byte("fake-png"),
		MimeType:      "image/png",
		Extension:     "png",
		RevisedPrompt: "internal expanded prompt should not be shown",
	}}
	var sentCaption string
	tool := &GenerateImageTool{
		chatID:    123,
		generator: stub,
		outputDir: t.TempDir(),
		notify:    func(int64, telebot.ChatAction) error { return nil },
		send: func(_ int64, _ string, caption string) error {
			sentCaption = caption
			return nil
		},
	}

	_, err := tool.Execute(context.Background(), `{"prompt":"нарисуй кота"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if sentCaption != "" {
		t.Fatalf("expected empty caption, got %q", sentCaption)
	}
}

func TestGenerateImageToolRetriesTransientGenerationError(t *testing.T) {
	stub := &imageGeneratorStub{
		resp: openaicodex.ImageGenerationResponse{
			Model:     "gpt-image-2",
			Image:     []byte("fake-png"),
			MimeType:  "image/png",
			Extension: "png",
		},
		errs: []error{
			fmt.Errorf("read codex image event stream: stream error: stream ID 7; INTERNAL_ERROR; received from peer"),
			nil,
		},
	}
	tool := &GenerateImageTool{
		chatID:    123,
		generator: stub,
		outputDir: t.TempDir(),
		notify:    func(int64, telebot.ChatAction) error { return nil },
		send:      func(int64, string, string) error { return nil },
	}

	if _, err := tool.Execute(context.Background(), `{"prompt":"нарисуй кота"}`); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(stub.requests) != 2 {
		t.Fatalf("expected retry after transient error, got %d requests", len(stub.requests))
	}
	if !tool.WasSent() {
		t.Fatal("expected image to be sent after retry")
	}
}
