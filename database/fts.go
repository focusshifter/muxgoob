package database

import (
	"database/sql"
	"fmt"
)

func EnsureMessageSearchIndex(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}

	statements := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			chat_id UNINDEXED,
			message_id UNINDEXED,
			text,
			caption,
			tokenize = 'unicode61'
		);`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, chat_id, message_id, text, caption)
			VALUES (new.rowid, new.chat_id, new.id, COALESCE(new.text, ''), COALESCE(new.caption, ''));
		END;`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, chat_id, message_id, text, caption)
			VALUES ('delete', old.rowid, old.chat_id, old.id, COALESCE(old.text, ''), COALESCE(old.caption, ''));
		END;`,
		`CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, chat_id, message_id, text, caption)
			VALUES ('delete', old.rowid, old.chat_id, old.id, COALESCE(old.text, ''), COALESCE(old.caption, ''));
			INSERT INTO messages_fts(rowid, chat_id, message_id, text, caption)
			VALUES (new.rowid, new.chat_id, new.id, COALESCE(new.text, ''), COALESCE(new.caption, ''));
		END;`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("create message search index objects: %w", err)
		}
	}

	var messageCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&messageCount); err != nil {
		return fmt.Errorf("count messages: %w", err)
	}

	var ftsCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages_fts`).Scan(&ftsCount); err != nil {
		return fmt.Errorf("count message search index rows: %w", err)
	}

	if messageCount == ftsCount {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin message search index rebuild: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(`DELETE FROM messages_fts`); err != nil {
		return fmt.Errorf("clear message search index: %w", err)
	}
	if _, err = tx.Exec(`
		INSERT INTO messages_fts(rowid, chat_id, message_id, text, caption)
		SELECT rowid, chat_id, id, COALESCE(text, ''), COALESCE(caption, '')
		FROM messages
	`); err != nil {
		return fmt.Errorf("rebuild message search index: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit message search index rebuild: %w", err)
	}

	return nil
}
