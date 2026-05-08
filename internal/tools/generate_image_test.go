package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
)

type imageGeneratorStub struct {
	requests []openaicodex.ImageGenerationRequest
	resp     openaicodex.ImageGenerationResponse
	err      error
}

func (s *imageGeneratorStub) GenerateImage(_ context.Context, request openaicodex.ImageGenerationRequest) (openaicodex.ImageGenerationResponse, error) {
	s.requests = append(s.requests, request)
	return s.resp, s.err
}

func TestGenerateImageToolExecuteGeneratesAndSendsImage(t *testing.T) {
	stub := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{
		Model:         "gpt-image-2",
		Image:         []byte("fake-png"),
		MimeType:      "image/png",
		Extension:     "png",
		RevisedPrompt: "a revised prompt",
	}}
	var sentPath string
	var sentCaption string
	tool := &GenerateImageTool{
		chatID:    123,
		generator: stub,
		outputDir: t.TempDir(),
		send: func(chatID int64, imagePath string, caption string) error {
			if chatID != 123 {
				t.Fatalf("expected chat id 123, got %d", chatID)
			}
			sentPath = imagePath
			sentCaption = caption
			return nil
		},
	}

	result, err := tool.Execute(context.Background(), `{"prompt":"нарисуй кота","size":"2048x2048","quality":"low","output_format":"png"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(stub.requests) != 1 {
		t.Fatalf("expected one generation request, got %d", len(stub.requests))
	}
	if stub.requests[0].Prompt != "нарисуй кота" || stub.requests[0].Model != "gpt-image-2" || stub.requests[0].Size != "2048x2048" {
		t.Fatalf("unexpected request: %#v", stub.requests[0])
	}
	if sentPath == "" {
		t.Fatal("expected image to be sent")
	}
	written, err := os.ReadFile(sentPath)
	if err != nil {
		t.Fatalf("read written image: %v", err)
	}
	if string(written) != "fake-png" {
		t.Fatalf("unexpected written image: %q", written)
	}
	if sentCaption != "Prompt: a revised prompt" {
		t.Fatalf("unexpected caption: %q", sentCaption)
	}
	if !tool.WasSent() {
		t.Fatal("expected tool to record successful send")
	}
	var payload generateImageResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !payload.Sent || payload.Model != "gpt-image-2" || payload.Path != sentPath {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
