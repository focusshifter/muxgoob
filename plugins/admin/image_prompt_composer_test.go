package admin

import (
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestHandleAiCommandsSetsChatImagePromptComposer(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	previousDB := database.DB
	database.DB = mockDB
	defer func() { database.DB = previousDB }()
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	registry.SetTestBot(&testutils.MockBotWrapper{})

	plugin := &AdminPlugin{}
	chatID := int64(-100123)
	chat := &telebot.Chat{ID: 1}
	for _, command := range []string{
		"!ai provider image-prompt openrouter -100123",
		"!ai model image-prompt nousresearch/hermes-4-70b -100123",
		"!ai image-prompt mode direct -100123",
	} {
		plugin.handleAiCommands(&telebot.Message{Text: command, Chat: chat})
	}
	if got := registry.GetImagePromptProvider(&chatID); got != "openrouter" {
		t.Fatalf("provider = %q", got)
	}
	if got := registry.GetImagePromptModel(&chatID); got != "nousresearch/hermes-4-70b" {
		t.Fatalf("model = %q", got)
	}
	if got := registry.GetImagePromptMode(&chatID); got != "direct" {
		t.Fatalf("mode = %q", got)
	}
}
