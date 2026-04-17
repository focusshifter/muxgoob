package main

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

type recordingPlugin struct {
	processed []*telebot.Message
}

func (p *recordingPlugin) Start(_ interface{}) {}
func (p *recordingPlugin) Process(message *telebot.Message) {
	p.processed = append(p.processed, message)
}

func createIncomingMessageSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT
		);
		CREATE TABLE IF NOT EXISTS chats (
			id INTEGER PRIMARY KEY,
			type TEXT,
			title TEXT,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT
		);
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER,
			chat_id INTEGER,
			sender_id INTEGER,
			reply_to_message_id INTEGER,
			forward_from_id INTEGER,
			forward_from_chat_id INTEGER,
			forward_date INTEGER,
			edit_date INTEGER,
			media_group_id TEXT,
			author_signature TEXT,
			unixtime INTEGER,
			text TEXT,
			caption TEXT,
			data TEXT,
			PRIMARY KEY (id, chat_id)
		);
		CREATE TABLE IF NOT EXISTS message_entities (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			offset INTEGER,
			length INTEGER,
			url TEXT,
			user_id INTEGER,
			language TEXT,
			is_caption BOOLEAN
		);
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			file_unique_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
}

func TestHandleIncomingMessageSavesPhotoMessagesAndDispatchesPlugins(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	createIncomingMessageSchema(t, mockDB)

	originalDB := database.DB
	database.DB = mockDB
	defer func() { database.DB = originalDB }()

	plugin := &recordingPlugin{}
	originalPlugins := registry.Plugins
	registry.Plugins = map[string]registry.MuxPlugin{"recording": plugin}
	defer func() { registry.Plugins = originalPlugins }()

	message := &telebot.Message{
		ID:      42,
		Chat:    &telebot.Chat{ID: 123, Type: telebot.ChatGroup, Title: "test"},
		Sender:  &telebot.User{ID: 7, Username: "alice", FirstName: "Alice"},
		Caption: "губи, смотри, это мем",
		Photo: &telebot.Photo{
			File:    telebot.File{FileID: "photo-file-id", FileSize: 1234},
			Width:   800,
			Height:  600,
			Caption: "губи, смотри, это мем",
		},
		Unixtime: time.Now().Unix(),
	}

	handleIncomingMessage(message)

	var caption string
	if err := mockDB.QueryRow(`SELECT caption FROM messages WHERE id = ? AND chat_id = ?`, message.ID, message.Chat.ID).Scan(&caption); err != nil {
		t.Fatalf("expected saved message, got error: %v", err)
	}
	if caption != message.Caption {
		t.Fatalf("expected caption %q, got %q", message.Caption, caption)
	}

	var mediaCount int
	if err := mockDB.QueryRow(`SELECT COUNT(*) FROM media_items WHERE message_id = ? AND chat_id = ? AND type = 'photo' AND file_id = ?`, message.ID, message.Chat.ID, message.Photo.FileID).Scan(&mediaCount); err != nil {
		t.Fatalf("count media items: %v", err)
	}
	if mediaCount != 1 {
		t.Fatalf("expected 1 saved photo media item, got %d", mediaCount)
	}

	if len(plugin.processed) == 0 {
		deadline := time.Now().Add(200 * time.Millisecond)
		for len(plugin.processed) == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if len(plugin.processed) != 1 || plugin.processed[0].ID != message.ID {
		t.Fatalf("expected plugin to process message once, got %+v", plugin.processed)
	}
}
