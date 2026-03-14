package version

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestReadCurrentVersion(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tempDir, ".muxgoob_version"), []byte("v0.6.3\n"), 0o644); err != nil {
		t.Fatalf("failed to write version file: %v", err)
	}

	versionText, err := readCurrentVersion()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if versionText != "v0.6.3" {
		t.Fatalf("expected v0.6.3, got %q", versionText)
	}
}

func TestNotifyOwnerVersion(t *testing.T) {
	oldBot := registry.Bot
	defer func() {
		registry.Bot = oldBot
	}()

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	err := notifyOwnerVersion(registry.Bot, 123456789, "v0.6.3")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !mockBot.SendCalled {
		t.Fatal("expected Send to be called")
	}

	chat, ok := mockBot.SendTo.(*telebot.Chat)
	if !ok {
		t.Fatalf("expected recipient to be *telebot.Chat, got %T", mockBot.SendTo)
	}

	if chat.ID != 123456789 {
		t.Fatalf("expected chat id 123456789, got %d", chat.ID)
	}

	message, ok := mockBot.SendWhat.(string)
	if !ok {
		t.Fatalf("expected string message, got %T", mockBot.SendWhat)
	}

	if message != "Gooby is now running v0.6.3" {
		t.Fatalf("unexpected notification message: %q", message)
	}
}

func TestLookupOwnerChatID(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()

	oldDB := database.DB
	defer func() {
		database.DB = oldDB
	}()

	database.DB = db
	createUsersTable(t, db)

	_, err := db.Exec("INSERT INTO users (id, username) VALUES (?, ?)", 123456789, "focusshifter")
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	ownerChatID, err := lookupOwnerChatID("focusshifter")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if ownerChatID != 123456789 {
		t.Fatalf("expected chat id 123456789, got %d", ownerChatID)
	}
}

func TestStartNotifiesOwnerFromUsersTable(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(tempDir, ".muxgoob_version"), []byte("v0.6.4\n"), 0o644); err != nil {
		t.Fatalf("failed to write version file: %v", err)
	}

	db := testutils.SetupTestDB(t)
	defer db.Close()
	oldDB := database.DB
	oldBot := registry.Bot

	database.DB = db
	createUsersTable(t, db)

	_, err = db.Exec("INSERT INTO users (id, username) VALUES (?, ?)", 123456789, "focusshifter")
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	oldConfig := registry.Config
	defer func() {
		registry.Config = oldConfig
		database.DB = oldDB
		registry.Bot = oldBot
	}()

	registry.Config.OwnerUsername = "focusshifter"

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &VersionPlugin{}
	plugin.Start(nil)

	if !mockBot.SendCalled {
		t.Fatal("expected Send to be called")
	}

	chat, ok := mockBot.SendTo.(*telebot.Chat)
	if !ok {
		t.Fatalf("expected recipient to be *telebot.Chat, got %T", mockBot.SendTo)
	}

	if chat.ID != 123456789 {
		t.Fatalf("expected chat id 123456789, got %d", chat.ID)
	}

	message, ok := mockBot.SendWhat.(string)
	if !ok {
		t.Fatalf("expected string message, got %T", mockBot.SendWhat)
	}

	if message != "Gooby is now running v0.6.4" {
		t.Fatalf("unexpected notification message: %q", message)
	}
}

func createUsersTable(t *testing.T, db *sql.DB) {
	t.Helper()

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("failed to create users table: %v", err)
	}
}
