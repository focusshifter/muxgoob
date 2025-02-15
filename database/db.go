package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

// TxFn is a function that will be called with an active transaction
type TxFn func(*sql.Tx) error

// WithTx executes the given function within a transaction
func WithTx(ctx context.Context, fn TxFn) error {
	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p) // re-throw panic after rollback
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

// RetryWithBackoff executes the given function with exponential backoff
func RetryWithBackoff(fn func() error) error {
	backoff := 10 * time.Millisecond
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}

		// Check for specific SQLite errors that indicate retrying would help
		errStr := err.Error()
		if errStr != "database is locked" && errStr != "database table is locked" && errStr != "busy" {
			return err
		}

		if i < maxRetries-1 { // Don't sleep on the last iteration
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return fmt.Errorf("max retries exceeded")
}

func Initialize() {
	var err error
	// Increased busy_timeout to 10 seconds and added other performance settings
	DB, err = sql.Open("sqlite3", "db/muxgoob.sqlite?_journal=WAL&_busy_timeout=10000&_synchronous=NORMAL&cache=shared&_txlock=immediate")
	if err != nil {
		log.Fatal("Failed to open SQLite DB:", err)
	}
	
	// Set connection pool settings
	DB.SetMaxOpenConns(2) // Allow 2 connections for better concurrency with WAL mode
	DB.SetMaxIdleConns(2)
	DB.SetConnMaxLifetime(time.Hour) // Recycle connections every hour

	// Create tables if they don't exist
	_, err = DB.Exec(`
		-- Core tables
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT  -- Full JSON for future compatibility
		);

		CREATE TABLE IF NOT EXISTS chats (
			id INTEGER PRIMARY KEY,
			type TEXT,
			title TEXT,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT  -- Full JSON for future compatibility
		);

		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER,
			chat_id INTEGER,
			sender_id INTEGER,  -- References users.id
			reply_to_message_id INTEGER,
			forward_from_id INTEGER,  -- References users.id
			forward_from_chat_id INTEGER,  -- References chats.id
			forward_date INTEGER,
			edit_date INTEGER,
			media_group_id TEXT,
			author_signature TEXT,
			unixtime INTEGER,
			text TEXT,
			caption TEXT,
			data TEXT,  -- Full JSON for future compatibility
			PRIMARY KEY (id, chat_id),
			FOREIGN KEY (chat_id) REFERENCES chats(id),
			FOREIGN KEY (sender_id) REFERENCES users(id),
			FOREIGN KEY (forward_from_id) REFERENCES users(id),
			FOREIGN KEY (forward_from_chat_id) REFERENCES chats(id)
		);

		-- Message content tables
		CREATE TABLE IF NOT EXISTS message_entities (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			offset INTEGER,
			length INTEGER,
			url TEXT,
			user_id INTEGER,  -- References users.id
			language TEXT,
			is_caption BOOLEAN,  -- true if entity belongs to caption
			FOREIGN KEY (message_id, chat_id) REFERENCES messages(id, chat_id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		);

		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,  -- photo, video, audio, document, sticker, etc.
			file_id TEXT,
			file_unique_id TEXT,
			width INTEGER,  -- for photos/videos
			height INTEGER,  -- for photos/videos
			duration INTEGER,  -- for audio/video
			file_name TEXT,
			mime_type TEXT,
			file_size INTEGER,
			thumb_file_id TEXT,
			data TEXT,  -- Full JSON for future compatibility
			FOREIGN KEY (message_id, chat_id) REFERENCES messages(id, chat_id)
		);

		-- Plugin-specific tables
		CREATE TABLE IF NOT EXISTS birthday_notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			year INTEGER,
			UNIQUE(username, year)
		);

		CREATE TABLE IF NOT EXISTS dupe_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT,
			message_id INTEGER,
			sender_id INTEGER,
			unixtime INTEGER,
			FOREIGN KEY (sender_id) REFERENCES users(id)
		);

		-- Twitch streams tables
		CREATE TABLE IF NOT EXISTS helix_streams (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_name TEXT,
			started_at DATETIME,
			data TEXT,  -- Full JSON for future compatibility
			UNIQUE(user_name)
		);

		CREATE TABLE IF NOT EXISTS helix_games (
			id TEXT PRIMARY KEY,  -- game_id from Twitch
			data TEXT  -- Full JSON for future compatibility
		);

		-- Stream notifications table
		CREATE TABLE IF NOT EXISTS stream_notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			stream_id TEXT UNIQUE,
			created_at INTEGER DEFAULT (strftime('%s', 'now'))
		);

		CREATE INDEX IF NOT EXISTS idx_messages_unixtime ON messages(unixtime);
		CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender_id);
		CREATE INDEX IF NOT EXISTS idx_messages_media_group ON messages(media_group_id);
		CREATE INDEX IF NOT EXISTS idx_media_items_file_id ON media_items(file_id);
		CREATE INDEX IF NOT EXISTS idx_dupe_links_url ON dupe_links(url);
		CREATE INDEX IF NOT EXISTS idx_birthday_notifications_username ON birthday_notifications(username);
		CREATE INDEX IF NOT EXISTS idx_helix_streams_user_name ON helix_streams(user_name);
		CREATE INDEX IF NOT EXISTS idx_stream_notifications_stream_id ON stream_notifications(stream_id);
	`)
	if err != nil {
		log.Fatal("Failed to create tables:", err)
	}
}

func Close() {
	if DB != nil {
		DB.Close()
	}
}
