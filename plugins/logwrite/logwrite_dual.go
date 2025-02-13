package logwrite

import (
	"encoding/json"
	"log"
	"strconv"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

type LogWriteDualPlugin struct {
	stormDb *storm.DB
}

func init() {
	registry.RegisterPlugin(&LogWriteDualPlugin{})
}

func (p *LogWriteDualPlugin) Start(sharedDb interface{}) {
	if sharedDb != nil {
		p.stormDb = sharedDb.(*storm.DB)
	}
}

func (p *LogWriteDualPlugin) Process(message *telebot.Message) {
	// Write to Storm DB if available
	if p.stormDb != nil {
		chat := p.stormDb.From(strconv.FormatInt(message.Chat.ID, 10))
		if err := chat.Save(message); err != nil {
			log.Println("Error saving message to Storm:", err)
		}

		chats := p.stormDb.From("chats")
		var existingChat telebot.Chat
		err := chats.One("ID", message.Chat.ID, &existingChat)
		if err != nil {
			if err := chats.Save(message.Chat); err != nil {
				log.Println("Error saving chat to Storm:", err)
			}
			log.Println("Chat list updated in Storm, new chat ID:", message.Chat.ID)
		}
	}

	// Write to SQLite
	tx, err := database.DB.Begin()
	if err != nil {
		log.Println("Error starting SQLite transaction:", err)
		return
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Save user if not exists
	userData, _ := json.Marshal(message.Sender)
	_, err = tx.Exec(
		"INSERT OR IGNORE INTO users (id, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?)",
		message.Sender.ID, message.Sender.Username, message.Sender.FirstName, message.Sender.LastName, string(userData))
	if err != nil {
		log.Println("Error saving user:", err)
		return
	}

	// Save chat if not exists
	chatData, _ := json.Marshal(message.Chat)
	_, err = tx.Exec(
		"INSERT OR IGNORE INTO chats (id, type, title, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
		message.Chat.ID, message.Chat.Type, message.Chat.Title, message.Chat.Username,
		message.Chat.FirstName, message.Chat.LastName, string(chatData))
	if err != nil {
		log.Println("Error saving chat:", err)
		return
	}

	// Save message
	msgData, _ := json.Marshal(message)
	_, err = tx.Exec(
		`INSERT OR REPLACE INTO messages (
			id, chat_id, sender_id, reply_to_message_id, forward_from_id,
			forward_from_chat_id, forward_date, edit_date, media_group_id,
			author_signature, unixtime, text, caption, data
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		message.ID, message.Chat.ID, message.Sender.ID,
		getMessageID(message.ReplyTo), getUserID(message.OriginalSender),
		getChatID(message.OriginalChat), message.OriginalUnixtime, message.LastEdit,
		message.AlbumID, message.Signature, message.Time().Unix(),
		message.Text, message.Caption, string(msgData))
	if err != nil {
		log.Println("Error saving message:", err)
		return
	}

	log.Println("Saved message from ", message.Sender.FirstName, message.Sender.LastName, ": ", message.Text)

	// Save message entities
	for _, entity := range message.Entities {
		_, err = tx.Exec(
			`INSERT OR REPLACE INTO message_entities (
				message_id, chat_id, type, offset, length, url, user_id, language, is_caption
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			message.ID, message.Chat.ID, entity.Type, entity.Offset, entity.Length,
			entity.URL, getUserID(entity.User), "", false)
		if err != nil {
			log.Println("Error saving message entity:", err)
			return
		}
	}

	// Save media items
	if message.Photo != nil {
		photoData, _ := json.Marshal(message.Photo)
		_, err = tx.Exec(
			`INSERT OR REPLACE INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, file_size, data
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			message.ID, message.Chat.ID, "photo", message.Photo.FileID, "",
			message.Photo.Width, message.Photo.Height, message.Photo.FileSize, string(photoData))
		if err != nil {
			log.Println("Error saving photo:", err)
			return
		}
	}

	err = tx.Commit()
	if err != nil {
		log.Println("Error committing transaction:", err)
		return
	}
}

func getMessageID(msg *telebot.Message) interface{} {
	if msg == nil {
		return nil
	}
	return msg.ID
}

func getUserID(user *telebot.User) interface{} {
	if user == nil {
		return nil
	}
	return user.ID
}

func getChatID(chat *telebot.Chat) interface{} {
	if chat == nil {
		return nil
	}
	return chat.ID
}
