package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Telegram API types based on official documentation
type TelegramUser struct {
	ID           int64  `json:"id"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name,omitempty"`
	Username     string `json:"username,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
	IsBot        bool   `json:"is_bot,omitempty"`
}

type TelegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

type TelegramMessageEntity struct {
	Type     string        `json:"type"`
	Offset   int           `json:"offset"`
	Length   int           `json:"length"`
	URL      string        `json:"url,omitempty"`
	User     *TelegramUser `json:"user,omitempty"`
	Language string        `json:"language,omitempty"`
}

type TelegramPhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size,omitempty"`
}

type TelegramAudio struct {
	FileID       string             `json:"file_id"`
	FileUniqueID string             `json:"file_unique_id"`
	Duration     int                `json:"duration"`
	Performer    string             `json:"performer,omitempty"`
	Title        string             `json:"title,omitempty"`
	FileName     string             `json:"file_name,omitempty"`
	MimeType     string             `json:"mime_type,omitempty"`
	FileSize     int                `json:"file_size,omitempty"`
	Thumbnail    *TelegramPhotoSize `json:"thumb,omitempty"`
}

type TelegramDocument struct {
	FileID       string             `json:"file_id"`
	FileUniqueID string             `json:"file_unique_id"`
	FileName     string             `json:"file_name,omitempty"`
	MimeType     string             `json:"mime_type,omitempty"`
	FileSize     int                `json:"file_size,omitempty"`
	Thumbnail    *TelegramPhotoSize `json:"thumb,omitempty"`
}

type TelegramVideo struct {
	FileID       string             `json:"file_id"`
	FileUniqueID string             `json:"file_unique_id"`
	Width        int                `json:"width"`
	Height       int                `json:"height"`
	Duration     int                `json:"duration"`
	FileName     string             `json:"file_name,omitempty"`
	MimeType     string             `json:"mime_type,omitempty"`
	FileSize     int                `json:"file_size,omitempty"`
	Thumbnail    *TelegramPhotoSize `json:"thumb,omitempty"`
}

type TelegramSticker struct {
	FileID       string             `json:"file_id"`
	FileUniqueID string             `json:"file_unique_id"`
	Width        int                `json:"width"`
	Height       int                `json:"height"`
	IsAnimated   bool               `json:"is_animated,omitempty"`
	IsVideo      bool               `json:"is_video,omitempty"`
	Thumbnail    *TelegramPhotoSize `json:"thumb,omitempty"`
	Emoji        string             `json:"emoji,omitempty"`
	SetName      string             `json:"set_name,omitempty"`
	FileSize     int                `json:"file_size,omitempty"`
}

type TelegramVoice struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int    `json:"file_size,omitempty"`
}

type TelegramVideoNote struct {
	FileID       string             `json:"file_id"`
	FileUniqueID string             `json:"file_unique_id"`
	Length       int                `json:"length"`
	Duration     int                `json:"duration"`
	Thumbnail    *TelegramPhotoSize `json:"thumb,omitempty"`
	FileSize     int                `json:"file_size,omitempty"`
}

type TelegramMessage struct {
	MessageID             int                     `json:"message_id"`
	From                  *TelegramUser           `json:"from,omitempty"`
	Date                  int                     `json:"date"`
	Chat                  TelegramChat            `json:"chat"`
	ForwardFrom           *TelegramUser           `json:"forward_from,omitempty"`
	ForwardFromChat       *TelegramChat           `json:"forward_from_chat,omitempty"`
	ForwardDate           int                     `json:"forward_date,omitempty"`
	ReplyToMessage        *TelegramMessage        `json:"reply_to_message,omitempty"`
	EditDate              int                     `json:"edit_date,omitempty"`
	MediaGroupID          string                  `json:"media_group_id,omitempty"`
	AuthorSignature       string                  `json:"author_signature,omitempty"`
	Text                  string                  `json:"text,omitempty"`
	Entities              []TelegramMessageEntity `json:"entities,omitempty"`
	CaptionEntities       []TelegramMessageEntity `json:"caption_entities,omitempty"`
	Audio                 *TelegramAudio          `json:"audio,omitempty"`
	Document              *TelegramDocument       `json:"document,omitempty"`
	Photo                 []TelegramPhotoSize     `json:"photo,omitempty"`
	Sticker               *TelegramSticker        `json:"sticker,omitempty"`
	Video                 *TelegramVideo          `json:"video,omitempty"`
	VideoNote             *TelegramVideoNote      `json:"video_note,omitempty"`
	Voice                 *TelegramVoice          `json:"voice,omitempty"`
	Caption               string                  `json:"caption,omitempty"`
	NewChatMembers        []TelegramUser          `json:"new_chat_members,omitempty"`
	LeftChatMember        *TelegramUser           `json:"left_chat_member,omitempty"`
	NewChatTitle          string                  `json:"new_chat_title,omitempty"`
	NewChatPhoto          []TelegramPhotoSize     `json:"new_chat_photo,omitempty"`
	DeleteChatPhoto       bool                    `json:"delete_chat_photo,omitempty"`
	GroupChatCreated      bool                    `json:"group_chat_created,omitempty"`
	SupergroupChatCreated bool                    `json:"supergroup_chat_created,omitempty"`
	ChannelChatCreated    bool                    `json:"channel_chat_created,omitempty"`
	MigrateToChatID       int64                   `json:"migrate_to_chat_id,omitempty"`
	MigrateFromChatID     int64                   `json:"migrate_from_chat_id,omitempty"`
	PinnedMessage         *TelegramMessage        `json:"pinned_message,omitempty"`
}

func main() {
	var (
		inputFile string
		dbFile    string
		dryRun    bool
	)

	flag.StringVar(&inputFile, "input", "", "Path to Telegram JSON file or directory containing JSON files")
	flag.StringVar(&dbFile, "db", "db/muxgoob.sqlite", "Path to SQLite database file")
	flag.BoolVar(&dryRun, "dry-run", false, "Parse but don't insert into database")
	flag.Parse()

	if inputFile == "" {
		log.Fatal("Input file or directory is required")
	}

	// Check if input is a file or directory
	fileInfo, err := os.Stat(inputFile)
	if err != nil {
		log.Fatalf("Error accessing input: %v", err)
	}

	var filesToProcess []string
	if fileInfo.IsDir() {
		// Process all JSON files in directory
		files, err := ioutil.ReadDir(inputFile)
		if err != nil {
			log.Fatalf("Error reading directory: %v", err)
		}
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
				filesToProcess = append(filesToProcess, filepath.Join(inputFile, file.Name()))
			}
		}
		if len(filesToProcess) == 0 {
			log.Fatal("No JSON files found in directory")
		}
	} else {
		filesToProcess = append(filesToProcess, inputFile)
	}

	// Open database connection
	var db *sql.DB
	if !dryRun {
		db, err = sql.Open("sqlite3", dbFile+"?_journal=WAL&_busy_timeout=10000&_synchronous=NORMAL&cache=shared")
		if err != nil {
			log.Fatalf("Failed to open database: %v", err)
		}
		defer db.Close()
	}

	// Process each file
	for _, file := range filesToProcess {
		fmt.Printf("Processing file: %s\n", file)

		data, err := ioutil.ReadFile(file)
		if err != nil {
			log.Printf("Error reading file %s: %v", file, err)
			continue
		}

		// Try to parse as a single message
		var message TelegramMessage
		err = json.Unmarshal(data, &message)
		if err == nil && message.MessageID != 0 {
			if dryRun {
				fmt.Printf("Would import message ID: %d\n", message.MessageID)
			} else {
				importMessage(db, &message)
			}
			continue
		}

		// Try to parse as an array of messages
		var messages []TelegramMessage
		err = json.Unmarshal(data, &messages)
		if err == nil {
			fmt.Printf("Found %d messages in file\n", len(messages))
			for i, msg := range messages {
				if dryRun {
					fmt.Printf("Would import message %d/%d: ID %d\n", i+1, len(messages), msg.MessageID)
				} else {
					importMessage(db, &msg)
				}
			}
			continue
		}

		// Try to parse as a map with a "messages" key (standard Telegram format)
		var messageMap map[string][]TelegramMessage
		err = json.Unmarshal(data, &messageMap)
		if err == nil {
			if msgs, ok := messageMap["messages"]; ok {
				fmt.Printf("Found %d messages in file under 'messages' key\n", len(msgs))
				for i, msg := range msgs {
					if dryRun {
						fmt.Printf("Would import message %d/%d: ID %d\n", i+1, len(msgs), msg.MessageID)
					} else {
						importMessage(db, &msg)
					}
				}
				continue
			}
		}

		// Try to parse as Telegram Desktop export format using a more flexible approach
		// Use map[string]interface{} to handle varying field types
		var exportData struct {
			Name     string                   `json:"name"`
			Type     string                   `json:"type"`
			ID       int64                    `json:"id"`
			Messages []map[string]interface{} `json:"messages"`
		}

		err = json.Unmarshal(data, &exportData)
		if err != nil {
			// Print the error to help with debugging
			fmt.Printf("Error parsing Telegram Desktop export format: %v\n", err)
		} else if len(exportData.Messages) > 0 {
			fmt.Printf("Found %d messages in Telegram Desktop export format\n", len(exportData.Messages))

			for i, exportMsg := range exportData.Messages {
				// Skip non-message types
				if msgType, ok := exportMsg["type"].(string); ok && msgType != "message" {
					continue
				}

				// Extract message ID
				msgID := 0
				if id, ok := exportMsg["id"].(float64); ok {
					msgID = int(id)
				}

				// Extract date
				date := 0
				if dateUnixtime, ok := exportMsg["date_unixtime"].(string); ok {
					date = parseUnixtime(dateUnixtime)
				}

				// Extract text
				text := ""
				if msgText, ok := exportMsg["text"].(string); ok {
					text = msgText
				}

				// Create message
				msg := TelegramMessage{
					MessageID: msgID,
					Date:      date,
					Text:      text,
					Chat: TelegramChat{
						ID:    exportData.ID,
						Type:  exportData.Type,
						Title: exportData.Name,
					},
				}

				// Create a basic user from the from_id field
				if fromID, ok := exportMsg["from_id"].(string); ok && fromID != "" {
					userID := parseUserID(fromID)
					fromName := ""
					if name, ok := exportMsg["from"].(string); ok {
						fromName = name
					}
					msg.From = &TelegramUser{
						ID:        userID,
						FirstName: fromName,
					}
				}

				if dryRun {
					fmt.Printf("Would import message %d/%d: ID %d from %s\n",
						i+1, len(exportData.Messages), msg.MessageID, exportMsg["from"])
				} else {
					importMessage(db, &msg)
				}
			}
			continue
		}

		log.Printf("Could not parse file %s as Telegram messages", file)
	}
}

func importMessage(db *sql.DB, message *TelegramMessage) {
	// Begin transaction
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Failed to begin transaction: %v", err)
		return
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			log.Printf("Transaction rolled back due to panic: %v", r)
		}
	}()

	// 1. Insert or update chat
	chatID := message.Chat.ID
	_, err = tx.Exec(`
		INSERT INTO chats (id, type, title, username, first_name, last_name, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			type = COALESCE(excluded.type, type),
			title = COALESCE(excluded.title, title),
			username = COALESCE(excluded.username, username),
			first_name = COALESCE(excluded.first_name, first_name),
			last_name = COALESCE(excluded.last_name, last_name),
			data = COALESCE(excluded.data, data)
	`,
		chatID,
		message.Chat.Type,
		message.Chat.Title,
		message.Chat.Username,
		message.Chat.FirstName,
		message.Chat.LastName,
		chatDataJSON(message.Chat))

	if err != nil {
		tx.Rollback()
		log.Printf("Failed to insert chat: %v", err)
		return
	}

	// 2. Insert or update sender (if exists)
	var senderID sql.NullInt64
	if message.From != nil {
		senderID.Int64 = message.From.ID
		senderID.Valid = true

		_, err = tx.Exec(`
			INSERT INTO users (id, username, first_name, last_name, data)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				username = COALESCE(excluded.username, username),
				first_name = COALESCE(excluded.first_name, first_name),
				last_name = COALESCE(excluded.last_name, last_name),
				data = COALESCE(excluded.data, data)
		`,
			message.From.ID,
			message.From.Username,
			message.From.FirstName,
			message.From.LastName,
			userDataJSON(message.From))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert sender: %v", err)
			return
		}
	}

	// 3. Handle forward_from if exists
	var forwardFromID sql.NullInt64
	if message.ForwardFrom != nil {
		forwardFromID.Int64 = message.ForwardFrom.ID
		forwardFromID.Valid = true

		_, err = tx.Exec(`
			INSERT INTO users (id, username, first_name, last_name, data)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				username = COALESCE(excluded.username, username),
				first_name = COALESCE(excluded.first_name, first_name),
				last_name = COALESCE(excluded.last_name, last_name),
				data = COALESCE(excluded.data, data)
		`,
			message.ForwardFrom.ID,
			message.ForwardFrom.Username,
			message.ForwardFrom.FirstName,
			message.ForwardFrom.LastName,
			userDataJSON(message.ForwardFrom))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert forward_from user: %v", err)
			return
		}
	}

	// 4. Handle forward_from_chat if exists
	var forwardFromChatID sql.NullInt64
	if message.ForwardFromChat != nil {
		forwardFromChatID.Int64 = message.ForwardFromChat.ID
		forwardFromChatID.Valid = true

		_, err = tx.Exec(`
			INSERT INTO chats (id, type, title, username, first_name, last_name, data)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				type = COALESCE(excluded.type, type),
				title = COALESCE(excluded.title, title),
				username = COALESCE(excluded.username, username),
				first_name = COALESCE(excluded.first_name, first_name),
				last_name = COALESCE(excluded.last_name, last_name),
				data = COALESCE(excluded.data, data)
		`,
			message.ForwardFromChat.ID,
			message.ForwardFromChat.Type,
			message.ForwardFromChat.Title,
			message.ForwardFromChat.Username,
			message.ForwardFromChat.FirstName,
			message.ForwardFromChat.LastName,
			chatDataJSON(*message.ForwardFromChat))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert forward_from_chat: %v", err)
			return
		}
	}

	// 5. Handle reply_to_message if exists
	var replyToMessageID sql.NullInt64
	if message.ReplyToMessage != nil {
		replyToMessageID.Int64 = int64(message.ReplyToMessage.MessageID)
		replyToMessageID.Valid = true

		// Recursively import the replied message first
		importMessage(db, message.ReplyToMessage)
	}

	// 6. Insert the message
	messageDataJSON, err := json.Marshal(message)
	if err != nil {
		tx.Rollback()
		log.Printf("Failed to marshal message data: %v", err)
		return
	}

	_, err = tx.Exec(`
		INSERT INTO messages (
			id, chat_id, sender_id, reply_to_message_id, 
			forward_from_id, forward_from_chat_id, forward_date, 
			edit_date, media_group_id, author_signature, 
			unixtime, text, caption, data
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_id) DO UPDATE SET
			sender_id = COALESCE(excluded.sender_id, sender_id),
			reply_to_message_id = COALESCE(excluded.reply_to_message_id, reply_to_message_id),
			forward_from_id = COALESCE(excluded.forward_from_id, forward_from_id),
			forward_from_chat_id = COALESCE(excluded.forward_from_chat_id, forward_from_chat_id),
			forward_date = COALESCE(excluded.forward_date, forward_date),
			edit_date = COALESCE(excluded.edit_date, edit_date),
			media_group_id = COALESCE(excluded.media_group_id, media_group_id),
			author_signature = COALESCE(excluded.author_signature, author_signature),
			unixtime = COALESCE(excluded.unixtime, unixtime),
			text = COALESCE(excluded.text, text),
			caption = COALESCE(excluded.caption, caption),
			data = COALESCE(excluded.data, data)
	`,
		message.MessageID,
		chatID,
		senderID,
		replyToMessageID,
		forwardFromID,
		forwardFromChatID,
		message.ForwardDate,
		message.EditDate,
		message.MediaGroupID,
		message.AuthorSignature,
		message.Date,
		message.Text,
		message.Caption,
		string(messageDataJSON))

	if err != nil {
		tx.Rollback()
		log.Printf("Failed to insert message: %v", err)
		return
	}

	// 7. Insert message entities if any
	if len(message.Entities) > 0 {
		for _, entity := range message.Entities {
			err = insertMessageEntity(tx, message.MessageID, chatID, entity, false)
			if err != nil {
				tx.Rollback()
				log.Printf("Failed to insert message entity: %v", err)
				return
			}
		}
	}

	// 8. Insert caption entities if any
	if len(message.CaptionEntities) > 0 {
		for _, entity := range message.CaptionEntities {
			err = insertMessageEntity(tx, message.MessageID, chatID, entity, true)
			if err != nil {
				tx.Rollback()
				log.Printf("Failed to insert caption entity: %v", err)
				return
			}
		}
	}

	// 9. Insert media items
	// Photo
	if len(message.Photo) > 0 {
		for _, photo := range message.Photo {
			err = insertMediaItem(tx, message.MessageID, chatID, "photo", photo)
			if err != nil {
				tx.Rollback()
				log.Printf("Failed to insert photo: %v", err)
				return
			}
		}
	}

	// Audio
	if message.Audio != nil {
		audioData, _ := json.Marshal(message.Audio)
		_, err = tx.Exec(`
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, duration, file_name, mime_type,
				file_size, thumb_file_id, data
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			message.MessageID,
			chatID,
			"audio",
			message.Audio.FileID,
			message.Audio.FileUniqueID,
			nil, // width
			nil, // height
			message.Audio.Duration,
			message.Audio.FileName,
			message.Audio.MimeType,
			message.Audio.FileSize,
			getThumbFileID(message.Audio.Thumbnail),
			string(audioData))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert audio: %v", err)
			return
		}
	}

	// Document
	if message.Document != nil {
		docData, _ := json.Marshal(message.Document)
		_, err = tx.Exec(`
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, duration, file_name, mime_type,
				file_size, thumb_file_id, data
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			message.MessageID,
			chatID,
			"document",
			message.Document.FileID,
			message.Document.FileUniqueID,
			nil, // width
			nil, // height
			nil, // duration
			message.Document.FileName,
			message.Document.MimeType,
			message.Document.FileSize,
			getThumbFileID(message.Document.Thumbnail),
			string(docData))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert document: %v", err)
			return
		}
	}

	// Video
	if message.Video != nil {
		videoData, _ := json.Marshal(message.Video)
		_, err = tx.Exec(`
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, duration, file_name, mime_type,
				file_size, thumb_file_id, data
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			message.MessageID,
			chatID,
			"video",
			message.Video.FileID,
			message.Video.FileUniqueID,
			message.Video.Width,
			message.Video.Height,
			message.Video.Duration,
			message.Video.FileName,
			message.Video.MimeType,
			message.Video.FileSize,
			getThumbFileID(message.Video.Thumbnail),
			string(videoData))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert video: %v", err)
			return
		}
	}

	// Sticker
	if message.Sticker != nil {
		stickerData, _ := json.Marshal(message.Sticker)
		_, err = tx.Exec(`
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, duration, file_name, mime_type,
				file_size, thumb_file_id, data
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			message.MessageID,
			chatID,
			"sticker",
			message.Sticker.FileID,
			message.Sticker.FileUniqueID,
			message.Sticker.Width,
			message.Sticker.Height,
			nil, // duration
			nil, // filename
			nil, // mime_type
			message.Sticker.FileSize,
			getThumbFileID(message.Sticker.Thumbnail),
			string(stickerData))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert sticker: %v", err)
			return
		}
	}

	// Voice
	if message.Voice != nil {
		voiceData, _ := json.Marshal(message.Voice)
		_, err = tx.Exec(`
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, duration, file_name, mime_type,
				file_size, thumb_file_id, data
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			message.MessageID,
			chatID,
			"voice",
			message.Voice.FileID,
			message.Voice.FileUniqueID,
			nil, // width
			nil, // height
			message.Voice.Duration,
			nil, // filename
			message.Voice.MimeType,
			message.Voice.FileSize,
			nil, // thumb_file_id
			string(voiceData))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert voice: %v", err)
			return
		}
	}

	// VideoNote
	if message.VideoNote != nil {
		videoNoteData, _ := json.Marshal(message.VideoNote)
		_, err = tx.Exec(`
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, duration, file_name, mime_type,
				file_size, thumb_file_id, data
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			message.MessageID,
			chatID,
			"video_note",
			message.VideoNote.FileID,
			message.VideoNote.FileUniqueID,
			message.VideoNote.Length, // width (same as length for video notes)
			message.VideoNote.Length, // height (same as length for video notes)
			message.VideoNote.Duration,
			nil, // filename
			nil, // mime_type
			message.VideoNote.FileSize,
			getThumbFileID(message.VideoNote.Thumbnail),
			string(videoNoteData))

		if err != nil {
			tx.Rollback()
			log.Printf("Failed to insert video note: %v", err)
			return
		}
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		log.Printf("Failed to commit transaction: %v", err)
		return
	}

	fmt.Printf("Successfully imported message ID %d in chat %d\n", message.MessageID, chatID)
}

func insertMessageEntity(tx *sql.Tx, messageID int, chatID int64, entity TelegramMessageEntity, isCaption bool) error {
	var userID sql.NullInt64
	if entity.User != nil {
		userID.Int64 = entity.User.ID
		userID.Valid = true

		// Insert the user first
		_, err := tx.Exec(`
			INSERT INTO users (id, username, first_name, last_name, data)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				username = COALESCE(excluded.username, username),
				first_name = COALESCE(excluded.first_name, first_name),
				last_name = COALESCE(excluded.last_name, last_name),
				data = COALESCE(excluded.data, data)
		`,
			entity.User.ID,
			entity.User.Username,
			entity.User.FirstName,
			entity.User.LastName,
			userDataJSON(entity.User))

		if err != nil {
			return err
		}
	}

	_, err := tx.Exec(`
		INSERT INTO message_entities (
			message_id, chat_id, type, offset, length,
			url, user_id, language, is_caption
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		messageID,
		chatID,
		entity.Type,
		entity.Offset,
		entity.Length,
		entity.URL,
		userID,
		entity.Language,
		isCaption)

	return err
}

func insertMediaItem(tx *sql.Tx, messageID int, chatID int64, mediaType string, photo TelegramPhotoSize) error {
	photoData, _ := json.Marshal(photo)
	_, err := tx.Exec(`
		INSERT INTO media_items (
			message_id, chat_id, type, file_id, file_unique_id,
			width, height, duration, file_name, mime_type,
			file_size, thumb_file_id, data
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		messageID,
		chatID,
		mediaType,
		photo.FileID,
		photo.FileUniqueID,
		photo.Width,
		photo.Height,
		nil, // duration
		nil, // filename
		nil, // mime_type
		photo.FileSize,
		nil, // thumb_file_id
		string(photoData))

	return err
}

func getThumbFileID(thumb *TelegramPhotoSize) sql.NullString {
	var thumbFileID sql.NullString
	if thumb != nil {
		thumbFileID.String = thumb.FileID
		thumbFileID.Valid = true
	}
	return thumbFileID
}

func userDataJSON(user *TelegramUser) string {
	if user == nil {
		return ""
	}
	data, err := json.Marshal(user)
	if err != nil {
		return ""
	}
	return string(data)
}

func chatDataJSON(chat TelegramChat) string {
	data, err := json.Marshal(chat)
	if err != nil {
		return ""
	}
	return string(data)
}

// parseUnixtime converts a string unixtime to an integer
func parseUnixtime(unixtimeStr string) int {
	unixtime := 0
	if unixtimeStr != "" {
		_, err := fmt.Sscanf(unixtimeStr, "%d", &unixtime)
		if err != nil {
			log.Printf("Error parsing unixtime %s: %v", unixtimeStr, err)
		}
	}
	return unixtime
}

// parseUserID extracts the numeric user ID from a string like "user123456789"
func parseUserID(userIDStr string) int64 {
	var userID int64

	// Remove any non-digit prefix (like "user")
	idStr := ""
	for _, c := range userIDStr {
		if c >= '0' && c <= '9' {
			idStr += string(c)
		}
	}

	if idStr != "" {
		_, err := fmt.Sscanf(idStr, "%d", &userID)
		if err != nil {
			log.Printf("Error parsing user ID %s: %v", userIDStr, err)
		}
	}

	return userID
}
