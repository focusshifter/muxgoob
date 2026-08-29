package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tucnak/telebot"
)

func setupMessageStoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, first_name TEXT, last_name TEXT, data TEXT);
		CREATE TABLE chats (id INTEGER PRIMARY KEY, type TEXT, title TEXT, username TEXT, first_name TEXT, last_name TEXT, data TEXT);
		CREATE TABLE messages (
			id INTEGER, chat_id INTEGER, sender_id INTEGER, reply_to_message_id INTEGER,
			forward_from_id INTEGER, forward_from_chat_id INTEGER, forward_date INTEGER,
			edit_date INTEGER, media_group_id TEXT, author_signature TEXT, unixtime INTEGER,
			text TEXT, caption TEXT, data TEXT, PRIMARY KEY (id, chat_id),
			FOREIGN KEY (chat_id) REFERENCES chats(id),
			FOREIGN KEY (sender_id) REFERENCES users(id),
			FOREIGN KEY (forward_from_id) REFERENCES users(id),
			FOREIGN KEY (forward_from_chat_id) REFERENCES chats(id)
		);
		CREATE TABLE message_entities (
			message_id INTEGER, chat_id INTEGER, type TEXT, offset INTEGER, length INTEGER,
			url TEXT, user_id INTEGER, language TEXT, is_caption BOOLEAN,
			FOREIGN KEY (message_id, chat_id) REFERENCES messages(id, chat_id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		);
		CREATE TABLE media_items (
			message_id INTEGER, chat_id INTEGER, type TEXT, file_id TEXT, file_unique_id TEXT,
			width INTEGER, height INTEGER, duration INTEGER, file_name TEXT, mime_type TEXT,
			file_size INTEGER, thumb_file_id TEXT, data TEXT,
			FOREIGN KEY (message_id, chat_id) REFERENCES messages(id, chat_id)
		);
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func TestSaveIncomingMessageIsIdempotentAndReplacesChildren(t *testing.T) {
	db := setupMessageStoreTestDB(t)
	defer db.Close()

	msg := &telebot.Message{
		ID:       7,
		Chat:     &telebot.Chat{ID: 10, Type: telebot.ChatGroup, Title: "Old"},
		Sender:   &telebot.User{ID: 20, Username: "old", FirstName: "Alice"},
		Text:     "before",
		Unixtime: 100,
		Entities: []telebot.MessageEntity{{Type: telebot.EntityURL, Offset: 0, Length: 6}},
		Photo:    &telebot.Photo{File: telebot.File{FileID: "photo-old", FileSize: 11}, Width: 10, Height: 20},
	}
	if changed, err := SaveIncomingMessage(context.Background(), db, msg); err != nil || !changed {
		t.Fatalf("first save: %v", err)
	}

	msg.Sender.Username = "new"
	msg.Chat.Title = "New"
	msg.Text = "after"
	msg.LastEdit = 200
	msg.Entities = nil
	msg.Caption = "caption"
	msg.CaptionEntities = []telebot.MessageEntity{{Type: telebot.EntityTextLink, Offset: 0, Length: 7, URL: "https://example.com"}}
	msg.Photo = &telebot.Photo{File: telebot.File{FileID: "photo-new", FileSize: 22}, Width: 30, Height: 40}
	if changed, err := SaveIncomingMessage(context.Background(), db, msg); err != nil || !changed {
		t.Fatalf("second save: %v", err)
	}
	if changed, err := SaveIncomingMessage(context.Background(), db, msg); err != nil || changed {
		t.Fatalf("duplicate save should be a no-op: changed=%v err=%v", changed, err)
	}

	var username, title, text string
	if err := db.QueryRow(`SELECT u.username, c.title, m.text FROM messages m JOIN users u ON u.id=m.sender_id JOIN chats c ON c.id=m.chat_id WHERE m.id=7 AND m.chat_id=10`).Scan(&username, &title, &text); err != nil {
		t.Fatal(err)
	}
	if username != "new" || title != "New" || text != "after" {
		t.Fatalf("stale upsert values: username=%q title=%q text=%q", username, title, text)
	}

	var messages, entities, captionEntities, media int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM messages`:                            &messages,
		`SELECT COUNT(*) FROM message_entities`:                    &entities,
		`SELECT COUNT(*) FROM message_entities WHERE is_caption=1`: &captionEntities,
		`SELECT COUNT(*) FROM media_items`:                         &media,
	} {
		if err := db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if messages != 1 || entities != 1 || captionEntities != 1 || media != 1 {
		t.Fatalf("unexpected counts messages=%d entities=%d caption=%d media=%d", messages, entities, captionEntities, media)
	}
	var fileID string
	if err := db.QueryRow(`SELECT file_id FROM media_items`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if fileID != "photo-new" {
		t.Fatalf("expected replaced photo, got %q", fileID)
	}
}

func TestSaveIncomingMessageRollsBackOnChildFailure(t *testing.T) {
	db := setupMessageStoreTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_entities BEFORE INSERT ON message_entities BEGIN SELECT RAISE(ABORT, 'reject'); END;`); err != nil {
		t.Fatal(err)
	}
	msg := &telebot.Message{
		ID:       8,
		Chat:     &telebot.Chat{ID: 11},
		Sender:   &telebot.User{ID: 21},
		Text:     "must roll back",
		Entities: []telebot.MessageEntity{{Type: telebot.EntityURL, Length: 1}},
	}
	if _, err := SaveIncomingMessage(context.Background(), db, msg); err == nil {
		t.Fatal("expected child insert failure")
	}
	for _, table := range []string{"users", "chats", "messages", "message_entities", "media_items"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected rollback for %s, got %d rows", table, count)
		}
	}
}

func TestSaveIncomingMessageRejectsIncompleteMessage(t *testing.T) {
	db := setupMessageStoreTestDB(t)
	defer db.Close()
	for _, msg := range []*telebot.Message{nil, {}, {Sender: &telebot.User{ID: 1}}} {
		if _, err := SaveIncomingMessage(context.Background(), db, msg); err == nil {
			t.Fatalf("expected validation error for %#v", msg)
		}
	}
}

func TestSaveIncomingMessageUpsertsForwardAndEntityReferences(t *testing.T) {
	db := setupMessageStoreTestDB(t)
	defer db.Close()
	msg := &telebot.Message{
		ID:             10,
		Chat:           &telebot.Chat{ID: 100, Type: telebot.ChatGroup},
		Sender:         &telebot.User{ID: 1},
		OriginalSender: &telebot.User{ID: 2, Username: "forwarded"},
		OriginalChat:   &telebot.Chat{ID: -1002, Type: telebot.ChatChannel, Title: "source"},
		Entities:       []telebot.MessageEntity{{Type: telebot.EntityMention, Length: 4, User: &telebot.User{ID: 3, Username: "mentioned"}}},
	}
	if changed, err := SaveIncomingMessage(context.Background(), db, msg); err != nil || !changed {
		t.Fatalf("save referenced users/chats: changed=%v err=%v", changed, err)
	}
	var users, chats int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chats`).Scan(&chats); err != nil {
		t.Fatal(err)
	}
	if users != 3 || chats != 2 {
		t.Fatalf("unexpected references users=%d chats=%d", users, chats)
	}
}

func TestSaveIncomingMessageAcceptsChannelPostWithoutSender(t *testing.T) {
	db := setupMessageStoreTestDB(t)
	defer db.Close()
	msg := &telebot.Message{ID: 9, Chat: &telebot.Chat{ID: -1001, Type: telebot.ChatChannel}, Text: "channel post"}
	changed, err := SaveIncomingMessage(context.Background(), db, msg)
	if err != nil || !changed {
		t.Fatalf("save senderless channel post: changed=%v err=%v", changed, err)
	}
	var sender sql.NullInt64
	if err := db.QueryRow(`SELECT sender_id FROM messages WHERE id=? AND chat_id=?`, msg.ID, msg.Chat.ID).Scan(&sender); err != nil {
		t.Fatal(err)
	}
	if sender.Valid {
		t.Fatalf("expected NULL sender_id, got %d", sender.Int64)
	}
}
