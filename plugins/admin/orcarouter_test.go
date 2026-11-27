package admin

import (
	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	"github.com/tucnak/telebot"
	"strings"
	"testing"
)

func TestOrcaAdminScopedProviderValidationAndOwner(t *testing.T) {
	oldDB, oldConfig, oldBot := database.DB, registry.Config, registry.Bot
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() { db.Close(); database.DB = oldDB; registry.Config = oldConfig; registry.Bot = oldBot })
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	registry.Config.OwnerUsername = "owner"
	registry.Config.AiProvider = "openrouter"
	registry.SetTestBot(&testutils.MockBotWrapper{})
	plugin := &AdminPlugin{}
	id, other := int64(-100123), int64(-100124)
	for _, tc := range []struct {
		command string
		get     func(*int64) string
	}{
		{"!ai provider orcarouter -100123", registry.GetAiProvider},
		{"!ai provider image orcarouter -100123", registry.GetImageAiProvider},
		{"!ai provider image-prompt orcarouter -100123", registry.GetImagePromptProvider},
	} {
		msg := &telebot.Message{Text: tc.command, Chat: &telebot.Chat{ID: 1, Type: telebot.ChatPrivate}, Sender: &telebot.User{Username: "intruder"}}
		plugin.Process(msg)
		if tc.get(&id) == "orcarouter" {
			t.Fatal("non-owner changed setting")
		}
		msg.Sender.Username = "owner"
		msg.Chat.Type = telebot.ChatGroup
		plugin.Process(msg)
		if tc.get(&id) == "orcarouter" {
			t.Fatal("group changed setting")
		}
		msg.Chat.Type = telebot.ChatPrivate
		plugin.Process(msg)
		if tc.get(&id) != "orcarouter" {
			t.Fatalf("provider rejected: %s", tc.command)
		}
		if tc.get(&other) == "orcarouter" || tc.get(nil) == "orcarouter" {
			t.Fatal("chat scope leaked")
		}
		msg.Text = strings.Replace(tc.command, "orcarouter", "invalid", 1)
		plugin.Process(msg)
		if tc.get(&id) != "orcarouter" {
			t.Fatal("invalid provider accepted")
		}
	}
}
