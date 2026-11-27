package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/internal/orcarouterimage"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestOrcaRouterImageRoutingNeverUsesOpenRouterKey(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	previousDB, previousConfig := database.DB, registry.Config
	database.DB = db
	defer func() { database.DB, registry.Config = previousDB, previousConfig }()
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	registry.Config.OrcarouterApiKey = ""
	registry.Config.OpenrouterApiKey = "must-not-be-reused"
	chatID := int64(9871)
	if err := registry.SetImageAiProvider(&chatID, "orcarouter"); err != nil {
		t.Fatal(err)
	}
	generator := imageGeneratorForChat(chatID)
	if _, ok := generator.(*orcarouterimage.Client); !ok {
		t.Fatalf("wrong image generator: %T", generator)
	}
	_, err := generator.GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Model: "openai/gpt-image-1", Prompt: "cat"})
	if err == nil || !strings.Contains(err.Error(), "OrcaRouter API key is not configured") {
		t.Fatalf("expected missing OrcaRouter key, got %v", err)
	}
}
