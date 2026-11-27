package registry

import (
	"github.com/focusshifter/muxgoob/database"
	"testing"
)

func TestDefaultAiProviderIsCodexWithoutRewritingExplicitProvider(t *testing.T) {
	oldConfig, oldDB := Config, database.DB
	t.Cleanup(func() { Config = oldConfig; database.DB = oldDB })
	database.DB = nil
	Config.AiProvider = ""
	if got := GetAiProvider(nil); got != "openai-codex" {
		t.Fatalf("default=%q", got)
	}
	Config.AiProvider = "openrouter"
	if got := GetAiProvider(nil); got != "openrouter" {
		t.Fatalf("explicit provider rewritten: %q", got)
	}
}
