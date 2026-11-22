package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

func TestDeliveryFailureCannotRegenerateInSameRequest(t *testing.T) {
	gen := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{Image: recoveryPNG(t), Extension: "png"}}
	sends := 0
	tool := &GenerateImageTool{generator: gen, outputDir: t.TempDir(), notify: func(int64, telebot.ChatAction) error { return nil }, send: func(int64, string, string) error { sends++; return context.DeadlineExceeded }}
	for i := 0; i < 2; i++ {
		_, err := tool.Execute(context.Background(), `{"prompt":"captain"}`)
		if err == nil {
			t.Fatal("expected delivery or already-generated error")
		}
	}
	if len(gen.requests) != 1 || sends != 1 || tool.WasSent() {
		t.Fatalf("generations=%d sends=%d sent=%v", len(gen.requests), sends, tool.WasSent())
	}
}

func TestJPEGPreparationFailureFallsBackToOriginalDocument(t *testing.T) {
	old := registry.Bot
	t.Cleanup(func() { registry.Bot = old })
	gen := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{Image: []byte("unsupported encoded image"), Extension: "webp"}}
	calls := 0
	registry.Bot = &registry.BotWrapper{SendFunc: func(to telebot.Recipient, what interface{}, opts ...interface{}) (*telebot.Message, error) {
		calls++
		if to.Recipient() != "-123" {
			t.Fatal("wrong target")
		}
		switch p := what.(type) {
		case *telebot.Photo:
			if calls != 1 {
				t.Fatal("unexpected photo retry")
			}
			return nil, errors.New("api error: Bad Request: IMAGE_PROCESS_FAILED")
		case *telebot.Document:
			if calls != 2 || p.Caption != "captain" || !strings.HasSuffix(p.FileLocal, ".webp") {
				t.Fatalf("wrong document: %#v", p)
			}
			return &telebot.Message{ID: 456}, nil
		default:
			t.Fatalf("unexpected media %T", what)
		}
		return nil, errors.New("unexpected send")
	}}
	tool := &GenerateImageTool{chatID: -123, generator: gen, outputDir: t.TempDir(), notify: func(int64, telebot.ChatAction) error { return nil }}
	_, err := tool.Execute(context.Background(), `{"prompt":"captain","caption":"captain","output_format":"webp"}`)
	if err != nil || !tool.WasSent() || calls != 2 || len(gen.requests) != 1 {
		t.Fatalf("error=%v sent=%v sends=%d generations=%d", err, tool.WasSent(), calls, len(gen.requests))
	}
}
