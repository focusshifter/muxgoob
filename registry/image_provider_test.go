package registry

import (
	"testing"

	"github.com/focusshifter/muxgoob/database"
)

func TestGetImageAiProviderDefaultsToCodex(t *testing.T) {
	previousConfig := Config
	previousDB := database.DB
	Config.ImageAiProvider = ""
	database.DB = nil
	defer func() {
		Config = previousConfig
		database.DB = previousDB
	}()
	if got := GetImageAiProvider(nil); got != "openai-codex" {
		t.Fatalf("default image provider = %q, want openai-codex", got)
	}
}
