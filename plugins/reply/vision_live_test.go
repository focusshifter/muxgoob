package reply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

// Opt-in unpublished smoke: requires the local VISION CHECK fixture (7391,
// red square left / blue circle right) and an explicitly supplied protected
// CODEX_HOME containing auth.json. Never opens a DB, downloads Telegram media,
// publishes, or enables OpenRouter fallback. Example (no secrets on command line):
// GOOBY_VISION_SMOKE_IMAGE=/path/to/fixture.png CODEX_HOME=/protected/codex \
// GOOBY_VISION_SMOKE_MODEL=openai/gpt-6-luna GOFLAGS=-tags=sqlite_fts5 \
// mise exec -- go test ./plugins/reply -run '^TestVisionCodexLive$' -count=1 -v
func TestVisionCodexLive(t *testing.T) {
	fixture := os.Getenv("GOOBY_VISION_SMOKE_IMAGE")
	if fixture == "" {
		t.Skip("GOOBY_VISION_SMOKE_IMAGE not set; no network")
	}
	if strings.TrimSpace(os.Getenv("CODEX_HOME")) == "" {
		t.Fatal("explicit protected CODEX_HOME required")
	}
	model := strings.TrimSpace(os.Getenv("GOOBY_VISION_SMOKE_MODEL"))
	if model == "" {
		t.Fatal("GOOBY_VISION_SMOKE_MODEL required")
	}
	if !openaicodex.NormalizeConfiguredModel(model).UseCodex {
		t.Fatal("live smoke requires a Codex model")
	}
	oldConfig, oldDB := registry.Config, database.DB
	t.Cleanup(func() { registry.Config = oldConfig; database.DB = oldDB })
	database.DB = nil
	registry.Config.AiProvider = "openai-codex"
	registry.Config.ImageVisionModel = model
	registry.Config.OpenrouterApiKey = ""
	// Main chat model must have no influence on the independent vision route.
	registry.Config.AiModel = "google/gemini-4-neon"
	processed, err := preprocessVisionImage(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(processed))
	dataURL, err := buildVisionDataURL(processed)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"inspection", "metadata"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			response, _, err := createImageVisionCompletion(openaicodex.WithoutFallback(ctx), 0, func(model string) openai.ChatCompletionRequest {
				if path == "metadata" {
					return buildImageMetadataCompletionRequest(model, dataURL)
				}
				return buildImageVisionCompletionRequest(model, dataURL, "Прочитай весь видимый текст и опиши цвет, форму и расположение фигур.")
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Choices) == 0 {
				t.Fatal("no vision choices")
			}
			text := response.Choices[0].Message.Content
			lower := strings.ToLower(text)
			if !strings.Contains(text, "7391") || !strings.Contains(lower, "красн") || !strings.Contains(lower, "квадрат") || !(strings.Contains(lower, "син") || strings.Contains(lower, "голуб")) || !(strings.Contains(lower, "круг") || strings.Contains(lower, "окружност")) {
				t.Fatalf("fixture text/shapes not recognized: %s", text)
			}
			t.Logf("unpublished vision smoke path=%s configured_model=%s response_model=%s: %s", path, model, response.Model, text)
		})
	}
}
