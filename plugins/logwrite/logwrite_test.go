package logwrite

import (
	"database/sql"
	"testing"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestLogWriteDualPluginDoesNotOwnSQLitePersistence(t *testing.T) {
	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Create required tables
	createTables(t, mockDB)

	// Create plugin instance
	plugin := &LogWriteDualPlugin{}

	// Test message
	message := &telebot.Message{
		ID: 123,
		Sender: &telebot.User{
			ID:        456,
			Username:  "test_user",
			FirstName: "Test",
			LastName:  "User",
		},
		Chat: &telebot.Chat{
			ID:    789,
			Type:  telebot.ChatPrivate,
			Title: "Test Chat",
		},
		Text:     "Test message",
		Unixtime: time.Now().Unix(),
		Entities: []telebot.MessageEntity{
			{
				Type:   "url",
				Offset: 5,
				Length: 7,
			},
		},
	}

	// Process the message
	plugin.Process(message)

	for _, table := range []string{"users", "chats", "messages", "message_entities"} {
		var count int
		if err := mockDB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("logwrite must not duplicate central SQLite ingestion: %s has %d rows", table, count)
		}
	}
}

// Helper function to create the necessary tables for testing
func createTables(t *testing.T, db *sql.DB) {
	// Create users table
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
		t.Fatalf("Failed to create users table: %v", err)
	}

	// Create chats table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			id INTEGER PRIMARY KEY,
			type TEXT,
			title TEXT,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create chats table: %v", err)
	}

	// Create messages table
	_, err = db.Exec(`
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
	`)
	if err != nil {
		t.Fatalf("Failed to create messages table: %v", err)
	}

	// Create message_entities table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS message_entities (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			offset INTEGER,
			length INTEGER,
			url TEXT,
			user_id INTEGER,
			language TEXT,
			is_caption INTEGER DEFAULT 0,
			UNIQUE(message_id, chat_id, type, offset, length, is_caption)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create message_entities table: %v", err)
	}
}
