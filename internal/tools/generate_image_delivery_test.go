package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

func recoveryPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 16, 12))
	im.SetNRGBA(8, 6, color.NRGBA{R: 255, A: 128})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGenerateImageDeliveryRecovery(t *testing.T) {
	rejection := errors.New("api error: Bad Request: IMAGE_PROCESS_FAILED")
	timeout := errors.New("Post sendPhoto: context deadline exceeded")
	forbidden := errors.New("api error: Forbidden: bot was blocked by the user")
	cases := []struct {
		name    string
		errs    []error
		methods []string
		wantErr string
	}{
		{"original photo", []error{nil}, []string{"photo"}, ""},
		{"clean JPEG", []error{rejection, nil}, []string{"photo", "photo"}, ""},
		{"original document", []error{rejection, rejection, nil}, []string{"photo", "photo", "document"}, ""},
		{"document failure", []error{rejection, rejection, forbidden}, []string{"photo", "photo", "document"}, "document"},
		{"document timeout", []error{rejection, rejection, timeout}, []string{"photo", "photo", "document"}, "document"},
		{"document processing rejection is terminal", []error{rejection, rejection, rejection}, []string{"photo", "photo", "document"}, "document"},
		{"generic bad request", []error{errors.New("api error: Bad Request: caption is too long")}, []string{"photo"}, "photo"},
		{"first timeout", []error{timeout}, []string{"photo"}, "photo"},
		{"first permission", []error{forbidden}, []string{"photo"}, "photo"},
		{"retry timeout", []error{rejection, timeout}, []string{"photo", "photo"}, "JPEG photo"},
		{"retry permission", []error{rejection, forbidden}, []string{"photo", "photo"}, "JPEG photo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := recoveryPNG(t)
			gen := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{Image: original, Extension: "png", Model: "test"}}
			var paths []string
			oldBot := registry.Bot
			t.Cleanup(func() { registry.Bot = oldBot })
			registry.Bot = &registry.BotWrapper{SendFunc: func(to telebot.Recipient, what interface{}, opts ...interface{}) (*telebot.Message, error) {
				if to.Recipient() != "-123" {
					t.Fatalf("recipient = %s", to.Recipient())
				}
				i := len(paths)
				if i >= len(tc.methods) {
					t.Fatal("unexpected extra send")
				}
				var path, caption, method string
				switch m := what.(type) {
				case *telebot.Photo:
					path, caption, method = m.FileLocal, m.Caption, "photo"
				case *telebot.Document:
					path, caption, method = m.FileLocal, m.Caption, "document"
				default:
					t.Fatalf("unexpected payload %T", what)
				}
				if method != tc.methods[i] || caption != "капитан" {
					t.Fatalf("send %d: %s %q", i, method, caption)
				}
				paths = append(paths, path)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					if path == paths[0] {
						t.Fatal("overwrote original")
					}
					im, format, err := image.Decode(bytes.NewReader(data))
					if err != nil || format != "jpeg" {
						t.Fatalf("retry format %s: %v", format, err)
					}
					if im.Bounds() != image.Rect(0, 0, 16, 12) {
						t.Fatalf("bounds %v", im.Bounds())
					}
					r, g, b, _ := im.At(0, 0).RGBA()
					if r < 60000 || g < 60000 || b < 60000 {
						t.Fatal("transparent background not flattened to white")
					}
				} else if !bytes.Equal(data, original) {
					t.Fatal("original bytes changed")
				}
				if i == 2 && path != paths[0] {
					t.Fatal("document fallback must use the original file")
				}
				if tc.errs[i] != nil {
					return nil, tc.errs[i]
				}
				return &telebot.Message{ID: 456}, nil
			}}
			tool := &GenerateImageTool{chatID: -123, generator: gen, outputDir: t.TempDir(), notify: func(int64, telebot.ChatAction) error { return nil }}
			result, err := tool.Execute(context.Background(), `{"prompt":"captain","caption":"капитан"}`)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), "image delivery failed") || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "retained") || !strings.Contains(err.Error(), "Do not regenerate") {
					t.Fatalf("inaccurate failure: %v", err)
				}
				if tool.WasSent() {
					t.Fatal("failed send marked sent")
				}
			} else if err != nil || !tool.WasSent() || !strings.Contains(result, `"sent":true`) {
				t.Fatalf("success %q %v sent=%v", result, err, tool.WasSent())
			}
			if len(paths) != len(tc.methods) || len(gen.requests) != 1 {
				t.Fatalf("sends=%d generations=%d", len(paths), len(gen.requests))
			}
			data, err := os.ReadFile(paths[0])
			if err != nil || !bytes.Equal(data, original) {
				t.Fatal("original not retained")
			}
		})
	}
}

func TestGenerateImageFailureStages(t *testing.T) {
	for _, stage := range []string{"generation", "file preparation", "JPEG file preparation"} {
		t.Run(stage, func(t *testing.T) {
			gen := &imageGeneratorStub{resp: openaicodex.ImageGenerationResponse{Image: []byte("invalid image"), Extension: "png"}}
			dir := t.TempDir()
			if stage == "generation" {
				gen.err = errors.New("provider rejected prompt")
			}
			if stage == "file preparation" {
				dir = dir + "/file"
				if err := os.WriteFile(dir, []byte("block"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			sends := 0
			tool := &GenerateImageTool{generator: gen, outputDir: dir, notify: func(int64, telebot.ChatAction) error { return nil }, send: func(int64, string, string) error {
				sends++
				return errors.New("api error: Bad Request: IMAGE_PROCESS_FAILED")
			}}
			_, err := tool.Execute(context.Background(), `{"prompt":"captain"}`)
			if err == nil || !strings.Contains(err.Error(), stage+" failed") {
				t.Fatalf("stage %s error: %v", stage, err)
			}
			if tool.WasSent() || len(gen.requests) != 1 {
				t.Fatalf("sent=%v generations=%d", tool.WasSent(), len(gen.requests))
			}
			if stage == "JPEG file preparation" && (sends != 1 || !strings.Contains(err.Error(), "retained")) {
				t.Fatalf("sends=%d error=%v", sends, err)
			}
		})
	}
}

func TestGenerateImageDefinitionNoRegenerationOnDeliveryFailure(t *testing.T) {
	description := (&GenerateImageTool{}).Definition().Function.Description
	if !strings.Contains(description, "Do not regenerate") || !strings.Contains(description, "delivery") {
		t.Fatal("missing delivery guidance")
	}
}

func TestTelegramPhotoProcessingErrorClassification(t *testing.T) {
	rejection := errors.New("api error: Bad Request: IMAGE_PROCESS_FAILED")
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{rejection, true},
		{fmt.Errorf("send photo: %w", rejection), true},
		{errors.New("api error: Bad Request: PHOTO_INVALID_DIMENSIONS"), true},
		{errors.New("api error: Bad Request: PHOTO_INVALID"), true},
		{errors.New("api error: Bad Request: IMAGE_PROCESS_FAILED: timeout"), false},
		{errors.New("timeout while reporting api error: Bad Request: IMAGE_PROCESS_FAILED"), false},
		{errors.New("api error: Forbidden: IMAGE_PROCESS_FAILED"), false},
		{errors.New("IMAGE_PROCESS_FAILED"), false},
		{context.DeadlineExceeded, false},
	}
	for _, tc := range cases {
		if got := isTelegramPhotoProcessingError(tc.err); got != tc.want {
			t.Errorf("error %v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}

// Optional local-only smoke: no provider calls or Telegram sends.
func TestReencodeIncidentImages(t *testing.T) {
	paths := os.Getenv("GOOBY_IMAGE_REENCODE_FIXTURES")
	if paths == "" {
		t.Skip("set GOOBY_IMAGE_REENCODE_FIXTURES to local incident paths")
	}
	for _, path := range strings.Split(paths, ":") {
		t.Run(fmt.Sprintf("%s", path), func(t *testing.T) {
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source, _, err := image.Decode(bytes.NewReader(original))
			if err != nil {
				t.Fatal(err)
			}
			copyPath := t.TempDir() + "/original"
			if err := os.WriteFile(copyPath, original, 0600); err != nil {
				t.Fatal(err)
			}
			cleaned, err := reencodeImageJPEG(copyPath)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(cleaned)
			if err != nil {
				t.Fatal(err)
			}
			decoded, format, err := image.Decode(bytes.NewReader(data))
			if err != nil || format != "jpeg" || decoded.Bounds() != source.Bounds() {
				t.Fatalf("decode %s %v", format, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, original) {
				t.Fatal("incident source changed")
			}
			t.Logf("%s: %dx%d -> clean JPEG %d bytes; source unchanged", path, decoded.Bounds().Dx(), decoded.Bounds().Dy(), len(data))
		})
	}
}
