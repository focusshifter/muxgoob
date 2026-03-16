package selfprompt

import (
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestCompressionModelSetting(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	globalModel := "openrouter/google/gemini-2.5-flash"
	if err := SetCompressionModel(nil, globalModel); err != nil {
		t.Fatalf("SetCompressionModel global failed: %v", err)
	}
	if got := GetCompressionModel(nil); got != globalModel {
		t.Fatalf("expected global compression model %q, got %q", globalModel, got)
	}

	chatID := int64(42)
	chatModel := "gpt-4o-mini"
	if err := SetCompressionModel(&chatID, chatModel); err != nil {
		t.Fatalf("SetCompressionModel chat failed: %v", err)
	}
	if got := GetCompressionModel(&chatID); got != chatModel {
		t.Fatalf("expected chat compression model %q, got %q", chatModel, got)
	}
	if got := GetCompressionModel(nil); got != globalModel {
		t.Fatalf("expected global compression model to remain %q, got %q", globalModel, got)
	}
}

func TestShouldConsolidateFacts(t *testing.T) {
	if shouldConsolidateFacts("Identity:\n- short\n\nInterests:\n- short") {
		t.Fatal("did not expect short profile to consolidate")
	}
	big := "Identity:\n- a\n\nInterests:\n- one\n- two\n- three\n- four\n- five\n- six\n- seven\n- eight\n- nine\n- ten\n- eleven\n- twelve"
	if !shouldConsolidateFacts(big) {
		t.Fatal("expected large profile to need consolidation")
	}
}
