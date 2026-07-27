package admin

import (
	"strings"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestHandleAiCommandsSetsChatImageProvider(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	previousDB := database.DB
	database.DB = mockDB
	defer func() { database.DB = previousDB }()
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &AdminPlugin{}
	plugin.handleAiCommands(&telebot.Message{
		Text: "!ai provider image openrouter -100123",
		Chat: &telebot.Chat{ID: 1},
	})

	chatID := int64(-100123)
	if got := registry.GetImageAiProvider(&chatID); got != "openrouter" {
		t.Fatalf("image provider = %q, want openrouter", got)
	}
	message, ok := mockBot.SendWhat.(string)
	if !ok || !strings.Contains(message, "Image provider for chat -100123 set to: openrouter") {
		t.Fatalf("unexpected admin response: %#v", mockBot.SendWhat)
	}
}
