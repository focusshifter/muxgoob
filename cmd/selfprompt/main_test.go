package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestResetStoredStateArchivesStructuredMemory(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	originalDB := database.DB
	database.DB = db
	defer func() { database.DB = originalDB }()

	for _, statement := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, first_name TEXT, last_name TEXT, data TEXT)`,
		`CREATE TABLE prompts (id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, version INTEGER NOT NULL, prompt TEXT NOT NULL, created_at INTEGER NOT NULL, UNIQUE(chat_id,version))`,
		`CREATE TABLE person_facts (id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, user_id INTEGER NOT NULL, facts TEXT NOT NULL, version INTEGER NOT NULL, created_at INTEGER NOT NULL, UNIQUE(chat_id,user_id,version))`,
		`INSERT INTO users(id,username) VALUES(1,'alice'),(2,'bob')`,
		`INSERT INTO prompts(chat_id,version,prompt,created_at) VALUES(100,1,'Reply style:\n- terse',0)`,
		`INSERT INTO person_facts(chat_id,user_id,facts,version,created_at) VALUES(100,1,'legacy alice',1,0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := chatmemory.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memory_migration_scopes(chat_id,state,updated_at) VALUES(100,'cutover',1)`); err != nil {
		t.Fatal(err)
	}
	repo := chatmemory.NewRepository(db)
	alice := int64(1)
	bob := int64(2)
	for _, entry := range []chatmemory.Entry{
		{ChatID: 100, Kind: chatmemory.ChatLore, Body: "shared lore", SourceType: "test"},
		{ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &alice, Body: "alice fact", SourceType: "test"},
		{ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &bob, Body: "bob structured-only fact", SourceType: "test"},
	} {
		if _, _, err := repo.Add(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}

	if err := resetStoredState(100, &activeChatUser{ID: 1, Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	assertActiveMemoryCount(t, db, 100, "person_fact", 1, 0)
	assertActiveMemoryCount(t, db, 100, "person_fact", 2, 1)
	assertActiveMemoryCount(t, db, 100, "chat_lore", 0, 1)
	var latestLegacy string
	if err := db.QueryRow(`SELECT facts FROM person_facts WHERE chat_id=100 AND user_id=1 ORDER BY version DESC LIMIT 1`).Scan(&latestLegacy); err != nil {
		t.Fatal(err)
	}
	if latestLegacy != "legacy alice" {
		t.Fatalf("cutover reset unexpectedly rewrote preserved legacy facts: %q", latestLegacy)
	}

	if err := resetStoredState(100, nil); err != nil {
		t.Fatal(err)
	}
	assertActiveMemoryCount(t, db, 100, "person_fact", 2, 0)
	assertActiveMemoryCount(t, db, 100, "chat_lore", 0, 0)
	var latestPrompt string
	if err := db.QueryRow(`SELECT prompt FROM prompts WHERE chat_id=100 ORDER BY version DESC LIMIT 1`).Scan(&latestPrompt); err != nil {
		t.Fatal(err)
	}
	if latestPrompt != "" {
		t.Fatalf("expected empty prompt, got %q", latestPrompt)
	}

	if err := savePrompt(100, "Reply style:\n- direct\n\nStable context:\n- new structured lore\n\nAvoid:\n- spoilers"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT prompt FROM prompts WHERE chat_id=100 ORDER BY version DESC LIMIT 1`).Scan(&latestPrompt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(latestPrompt, "Stable context:") || strings.Contains(latestPrompt, "new structured lore") {
		t.Fatalf("cutover CLI save retained legacy stable context: %q", latestPrompt)
	}
	assertActiveMemoryCount(t, db, 100, "chat_lore", 0, 1)
}

func assertActiveMemoryCount(t *testing.T, db interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, chatID int64, kind string, subjectID int64, want int) {
	t.Helper()
	query := `SELECT COUNT(*) FROM memory_entries WHERE chat_id=? AND kind=? AND status='active'`
	args := []interface{}{chatID, kind}
	if subjectID != 0 {
		query += ` AND subject_user_id=?`
		args = append(args, subjectID)
	}
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("active %s count for subject %d: got %d want %d", kind, subjectID, got, want)
	}
}
