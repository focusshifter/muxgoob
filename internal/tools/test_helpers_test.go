package tools

import (
	"database/sql"
	"testing"
)

func createToolTestTables(t *testing.T, db *sql.DB) {
	t.Helper()

	statements := []string{
		`CREATE TABLE chats (id INTEGER PRIMARY KEY, type TEXT, title TEXT, username TEXT, first_name TEXT, last_name TEXT, data TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, first_name TEXT, last_name TEXT, data TEXT)`,
		`CREATE TABLE messages (id INTEGER NOT NULL, chat_id INTEGER NOT NULL, sender_id INTEGER, unixtime INTEGER, text TEXT, caption TEXT, data TEXT, PRIMARY KEY (id, chat_id))`,
		`CREATE TABLE prompts (id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, version INTEGER NOT NULL, prompt TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(chat_id, version))`,
		`CREATE TABLE person_facts (chat_id INTEGER NOT NULL, user_id INTEGER NOT NULL, facts TEXT NOT NULL, version INTEGER NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY (chat_id, user_id, version))`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("failed to create test table: %v", err)
		}
	}

	if _, err := db.Exec(`INSERT INTO chats (id, type, title) VALUES (100, 'group', 'Test')`); err != nil {
		t.Fatalf("failed to insert chat: %v", err)
	}
}

func insertUser(t *testing.T, db *sql.DB, id int64, username, firstName, lastName string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO users (id, username, first_name, last_name) VALUES (?, ?, ?, ?)`, id, username, firstName, lastName); err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
}

func insertMessage(t *testing.T, db *sql.DB, id, chatID, senderID int64, unixtime int64, text string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO messages (id, chat_id, sender_id, unixtime, text) VALUES (?, ?, ?, ?, ?)`, id, chatID, senderID, unixtime, text); err != nil {
		t.Fatalf("failed to insert message: %v", err)
	}
}

func insertPersonFacts(t *testing.T, db *sql.DB, chatID, userID int64, facts string, version int) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO person_facts (chat_id, user_id, facts, version, created_at) VALUES (?, ?, ?, ?, 0)`, chatID, userID, facts, version); err != nil {
		t.Fatalf("failed to insert person facts: %v", err)
	}
}

func insertPrompt(t *testing.T, db *sql.DB, chatID int64, version int, prompt string) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO prompts (chat_id, version, prompt, created_at) VALUES (?, ?, ?, 0)`, chatID, version, prompt); err != nil {
		t.Fatalf("failed to insert prompt: %v", err)
	}
}
