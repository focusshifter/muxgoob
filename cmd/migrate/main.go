package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/asdine/storm"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tucnak/telebot"
)

type DupeLink struct {
	ID        int    `storm:"id,increment"`
	URL       string `storm:"index"`
	MessageID int
	Sender    telebot.User
	Unixtime  int
}

func main() {
	// Open Storm DB
	stormDb, err := storm.Open("db/muxgoob.db")
	if err != nil {
		log.Fatal("Failed to open Storm DB:", err)
	}
	defer stormDb.Close()

	// Create SQLite DB
	if err := os.MkdirAll("db", 0755); err != nil {
		log.Fatal("Failed to create db directory:", err)
	}
	sqliteDb, err := sql.Open("sqlite3", "db/muxgoob.sqlite")
	if err != nil {
		log.Fatal("Failed to open SQLite DB:", err)
	}
	defer sqliteDb.Close()

	// Create tables
	_, err = sqliteDb.Exec(`
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

		-- Dupe links table
		CREATE TABLE IF NOT EXISTS  dupe_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT,
			message_id INTEGER,
			chat_id INTEGER,
			sender_id INTEGER,
			unixtime INTEGER,
			FOREIGN KEY (sender_id) REFERENCES users(id),
			FOREIGN KEY (chat_id) REFERENCES chats(id)
		);
		CREATE INDEX IF NOT EXISTS idx_dupe_links_url ON dupe_links(url);

		-- Indexes for better query performance
		CREATE INDEX IF NOT EXISTS idx_messages_unixtime ON messages(unixtime);
		CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender_id);
		CREATE INDEX IF NOT EXISTS idx_messages_media_group ON messages(media_group_id);
		CREATE INDEX IF NOT EXISTS idx_media_items_file_id ON media_items(file_id);
	`)
	if err != nil {
		log.Fatal("Failed to create tables:", err)
	}

	// Start transaction
	tx, err := sqliteDb.Begin()
	if err != nil {
		log.Fatal("Failed to start transaction:", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Prepare statements for better performance
	insertUser, err := tx.Prepare(`
		INSERT OR REPLACE INTO users (id, username, first_name, last_name, data)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		log.Fatal("Failed to prepare user statement:", err)
	}
	defer insertUser.Close()

	insertChat, err := tx.Prepare(`
		INSERT OR REPLACE INTO chats (id, type, title, username, first_name, last_name, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		log.Fatal("Failed to prepare chat statement:", err)
	}
	defer insertChat.Close()

	insertMessage, err := tx.Prepare(`
		INSERT OR REPLACE INTO messages (
			id, chat_id, sender_id, reply_to_message_id, forward_from_id, forward_from_chat_id,
			forward_date, edit_date, media_group_id, author_signature, unixtime, text, caption, data
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		log.Fatal("Failed to prepare message statement:", err)
	}
	defer insertMessage.Close()

	insertEntity, err := tx.Prepare(`
		INSERT INTO message_entities (message_id, chat_id, type, offset, length, url, user_id, language, is_caption)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		log.Fatal("Failed to prepare entity statement:", err)
	}
	defer insertEntity.Close()

	insertMedia, err := tx.Prepare(`
		INSERT INTO media_items (message_id, chat_id, type, file_id, file_unique_id, width, height,
			duration, file_name, mime_type, file_size, thumb_file_id, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		log.Fatal("Failed to prepare media statement:", err)
	}
	defer insertMedia.Close()

	// Migrate chats and their messages
	log.Println("Starting to fetch chats...")
	var chats []telebot.Chat
	err = stormDb.From("chats").All(&chats)
	if err != nil {
		if err == storm.ErrNotFound {
			log.Println("No chats found in the database")
			return
		} else {
			log.Fatal("Failed to fetch chats:", err)
		}
	}
	log.Printf("Found %d chats", len(chats))

	// No need to start another transaction here

	// Track users we've already saved to avoid duplicates
	processedUsers := make(map[int]bool)

	// Helper function to save a user
	saveUser := func(user *telebot.User) error {
		if user == nil || processedUsers[user.ID] {
			return nil
		}
		userData, _ := json.Marshal(user)
		_, err := insertUser.Exec(
			user.ID, user.Username, user.FirstName,
			user.LastName, string(userData),
		)
		if err != nil {
			return fmt.Errorf("failed to insert user %d: %v", user.ID, err)
		}
		processedUsers[user.ID] = true
		return nil
	}

	// Save chats
	for _, chat := range chats {
		chatData, _ := json.Marshal(chat)
		_, err = insertChat.Exec(
			chat.ID, chat.Type, chat.Title, chat.Username,
			chat.FirstName, chat.LastName, string(chatData),
		)
		if err != nil {
			log.Printf("Failed to insert chat %d: %v", chat.ID, err)
			continue
		}
	}

	// Migrate messages from each chat
	log.Println("Processing messages from each chat...")
	for _, chat := range chats {
		// Convert chat ID to string for bucket name
		bucket := strconv.FormatInt(chat.ID, 10)
		log.Printf("Processing messages from chat %s (%s)", chat.Title, bucket)

		var messages []telebot.Message
		err = stormDb.From(bucket).All(&messages)
		if err != nil {
			if err != storm.ErrNotFound {
				log.Printf("Failed to fetch messages from chat %s: %v", bucket, err)
			}
			continue
		}
		log.Printf("Found %d messages in chat %s", len(messages), bucket)

		for _, msg := range messages {
			// Save message sender
			if err := saveUser(msg.Sender); err != nil {
				log.Println(err)
			}

			// Save original sender if message is forwarded
			if err := saveUser(msg.OriginalSender); err != nil {
				log.Println(err)
			}

			// Save the message
			msgData, _ := json.Marshal(msg)
			unixtime := msg.Time().Unix()

			// Get sender ID safely
			var senderID interface{}
			if msg.Sender != nil {
				senderID = msg.Sender.ID
			}

			// Get forward from ID safely
			var forwardFromID interface{}
			if msg.OriginalSender != nil {
				forwardFromID = msg.OriginalSender.ID
			}

			// Get forward from chat ID safely
			var forwardFromChatID interface{}
			if msg.OriginalChat != nil {
				forwardFromChatID = msg.OriginalChat.ID
			}

			// Get reply to message ID safely
			var replyToMessageID interface{}
			if msg.ReplyTo != nil {
				replyToMessageID = msg.ReplyTo.ID
			}

			_, err = insertMessage.Exec(
				msg.ID, msg.Chat.ID, senderID, replyToMessageID,
				forwardFromID, forwardFromChatID, msg.OriginalUnixtime,
				msg.LastEdit, msg.AlbumID, msg.Signature, unixtime,
				msg.Text, msg.Caption, string(msgData),
			)
			if err != nil {
				log.Printf("Failed to insert message %d from chat %s: %v", msg.ID, bucket, err)
				continue
			}

			// Save message entities
			for _, entity := range msg.Entities {
				_, err = insertEntity.Exec(
					msg.ID, msg.Chat.ID, entity.Type, entity.Offset,
					entity.Length, entity.URL, nil, nil, false,
				)
				if err != nil {
					log.Printf("Failed to insert entity for message %d: %v", msg.ID, err)
				}
			}

			// Save caption entities
			for _, entity := range msg.CaptionEntities {
				_, err = insertEntity.Exec(
					msg.ID, msg.Chat.ID, entity.Type, entity.Offset,
					entity.Length, entity.URL, nil, nil, true,
				)
				if err != nil {
					log.Printf("Failed to insert caption entity for message %d: %v", msg.ID, err)
				}
			}

			// Helper function to save media items
			saveMedia := func(mediaType string, item interface{}, data []byte) {
				var fileID, fileUniqueID, fileName, mimeType, thumbFileID string
				var width, height, duration, fileSize int

				switch v := item.(type) {
				case *telebot.Photo:
					if v == nil {
						return
					}
					fileID = v.FileID
					width = v.Width
					height = v.Height
					fileSize = v.FileSize
				case *telebot.Audio:
					if v == nil {
						return
					}
					fileID = v.FileID
					duration = v.Duration
					fileSize = v.FileSize
				case *telebot.Document:
					if v == nil {
						return
					}
					fileID = v.FileID
					fileSize = v.FileSize
				case *telebot.Video:
					if v == nil {
						return
					}
					fileID = v.FileID
					width = v.Width
					height = v.Height
					duration = v.Duration
					fileSize = v.FileSize
				}

				_, err = insertMedia.Exec(
					msg.ID, msg.Chat.ID, mediaType, fileID, fileUniqueID,
					width, height, duration, fileName, mimeType,
					fileSize, thumbFileID, string(data),
				)
				if err != nil {
					log.Printf("Failed to insert media for message %d: %v", msg.ID, err)
				}
			}

			// Save media items
			if msg.Photo != nil {
				data, _ := json.Marshal(msg.Photo)
				saveMedia("photo", msg.Photo, data)
			}
			if msg.Audio != nil {
				data, _ := json.Marshal(msg.Audio)
				saveMedia("audio", msg.Audio, data)
			}
			if msg.Document != nil {
				data, _ := json.Marshal(msg.Document)
				saveMedia("document", msg.Document, data)
			}
			if msg.Video != nil {
				data, _ := json.Marshal(msg.Video)
				saveMedia("video", msg.Video, data)
			}
			if msg.Voice != nil {
				data, _ := json.Marshal(msg.Voice)
				saveMedia("voice", msg.Voice, data)
			}
			if msg.VideoNote != nil {
				data, _ := json.Marshal(msg.VideoNote)
				saveMedia("video_note", msg.VideoNote, data)
			}
			if msg.Sticker != nil {
				data, _ := json.Marshal(msg.Sticker)
				saveMedia("sticker", msg.Sticker, data)
			}
		}
	}

	// Migrate dupe_links
	log.Println("Migrating dupe_links...")

	insertDupeLink, err := tx.Prepare(`
		INSERT INTO dupe_links (url, message_id, chat_id, sender_id, unixtime)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		log.Fatal("Failed to prepare dupe_link statement:", err)
	}
	defer insertDupeLink.Close()

	// Migrate dupe_links from each chat bucket
	var totalDupeLinks int
	for _, chat := range chats {
		chatBucket := strconv.FormatInt(chat.ID, 10)
		var dupeLinks []DupeLink
		err = stormDb.From(chatBucket).All(&dupeLinks)
		if err != nil && err != storm.ErrNotFound {
			log.Printf("Failed to fetch dupe_links from chat %s: %v", chatBucket, err)
			continue
		}
		if err == nil && len(dupeLinks) > 0 {
			log.Printf("Found %d dupe_links in chat %s", len(dupeLinks), chatBucket)
			for _, link := range dupeLinks {
				_, err = insertDupeLink.Exec(
					link.URL, link.MessageID, chat.ID, link.Sender.ID, link.Unixtime,
				)
				if err != nil {
					log.Printf("Failed to insert dupe_link %s: %v", link.URL, err)
				} else {
					totalDupeLinks++
				}
			}
		}
	}
	log.Printf("Total dupe_links migrated: %d", totalDupeLinks)

	// Commit the transaction
	if err = tx.Commit(); err != nil {
		log.Fatal("Failed to commit transaction:", err)
	}

	// Verify the data was migrated
	var messageCount, chatCount, userCount, dupeLinkCount int
	err = sqliteDb.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messageCount)
	if err != nil {
		log.Fatal("Failed to count messages:", err)
	}

	err = sqliteDb.QueryRow("SELECT COUNT(*) FROM chats").Scan(&chatCount)
	if err != nil {
		log.Fatal("Failed to count chats:", err)
	}

	err = sqliteDb.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	if err != nil {
		log.Fatal("Failed to count users:", err)
	}

	err = sqliteDb.QueryRow("SELECT COUNT(*) FROM dupe_links").Scan(&dupeLinkCount)
	if err != nil {
		log.Fatal("Failed to count dupe_links:", err)
	}

	log.Printf("Migration completed successfully!")
	log.Printf("Migrated %d messages, %d chats, %d users, and %d dupe_links", messageCount, chatCount, userCount, dupeLinkCount)
}
