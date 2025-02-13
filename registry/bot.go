package registry

import (
	"encoding/json"
	"log"

	"github.com/focusshifter/muxgoob/database"
	"github.com/tucnak/telebot"
)

// BotWrapper wraps telebot.Bot to add message saving functionality
type BotWrapper struct {
	*telebot.Bot
}

// Send sends a message and saves it to the database
func (b *BotWrapper) Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
	msg, err := b.Bot.Send(to, what, options...)
	if err != nil {
		return msg, err
	}

	// Save bot's message to database
	if msg != nil {
		// Save bot user if not exists
		userData, _ := json.Marshal(msg.Sender)
		_, err = database.DB.Exec(
			"INSERT OR IGNORE INTO users (id, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?)",
			msg.Sender.ID, msg.Sender.Username, msg.Sender.FirstName, msg.Sender.LastName, string(userData))
		if err != nil {
			log.Printf("Error saving bot user: %v", err)
		}

		// Save chat if not exists
		chatData, _ := json.Marshal(msg.Chat)
		_, err = database.DB.Exec(
			"INSERT OR IGNORE INTO chats (id, type, title, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
			msg.Chat.ID, msg.Chat.Type, msg.Chat.Title, msg.Chat.Username,
			msg.Chat.FirstName, msg.Chat.LastName, string(chatData))
		if err != nil {
			log.Printf("Error saving chat: %v", err)
		}

		// Save message
		msgData, _ := json.Marshal(msg)
		_, err = database.DB.Exec(
			`INSERT INTO messages (
				id, chat_id, sender_id, reply_to_message_id, forward_from_id,
				forward_from_chat_id, forward_date, edit_date, media_group_id,
				author_signature, unixtime, text, caption, data
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.ID, msg.Chat.ID, msg.Sender.ID,
			getMessageID(msg.ReplyTo), getUserID(msg.OriginalSender),
			getChatID(msg.OriginalChat), msg.OriginalUnixtime, msg.LastEdit,
			msg.AlbumID, msg.Signature, msg.Time().Unix(),
			msg.Text, msg.Caption, string(msgData))
		if err != nil {
			log.Printf("Error saving bot message: %v", err)
		}

		// Save message entities
		for _, entity := range msg.Entities {
			_, err = database.DB.Exec(
				`INSERT INTO message_entities (
					message_id, chat_id, type, offset, length, url, user_id, language, is_caption
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				msg.ID, msg.Chat.ID, entity.Type, entity.Offset, entity.Length,
				entity.URL, getUserID(entity.User), "", false)
			if err != nil {
				log.Printf("Error saving bot message entity: %v", err)
			}
		}
	}

	return msg, err
}

// Helper functions copied from main.go
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
