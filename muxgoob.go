package main

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"

	_ "github.com/focusshifter/muxgoob/plugins/admin"
	_ "github.com/focusshifter/muxgoob/plugins/birthdays"
	_ "github.com/focusshifter/muxgoob/plugins/dupelink"
	_ "github.com/focusshifter/muxgoob/plugins/logwrite"
	_ "github.com/focusshifter/muxgoob/plugins/nametrigger"
	_ "github.com/focusshifter/muxgoob/plugins/reply"
	_ "github.com/focusshifter/muxgoob/plugins/twitchstreams"
)

var token string

func main() {
	log.Println("Rise and shine, Mux")

	token = os.Getenv("MUXGOOB_KEY")

	registry.LoadConfig("config.yml")

	// Initialize databases
	database.Initialize()
	defer database.DB.Close()

	// Initialize StormDB for legacy support
	stormDb, err := storm.Open("db/muxgoob.db")
	if err != nil {
		log.Fatal("Failed to open StormDB:", err)
	}
	defer stormDb.Close()

	bot, err := telebot.NewBot(telebot.Settings{
		Token:  registry.Config.TelegramKey,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Fatal(err)
	}

	registry.Bot = &registry.BotWrapper{Bot: bot}

	for _, d := range registry.Plugins {
		go d.Start(stormDb)
	}

	bot.Handle(telebot.OnText, func(message *telebot.Message) {
		// Save user if not exists
		userData, _ := json.Marshal(message.Sender)
		_, err = database.DB.Exec(
			"INSERT OR IGNORE INTO users (id, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?)",
			message.Sender.ID, message.Sender.Username, message.Sender.FirstName, message.Sender.LastName, string(userData))
		if err != nil {
			log.Printf("Error saving user: %v", err)
		}

		// Save chat if not exists
		chatData, _ := json.Marshal(message.Chat)
		_, err = database.DB.Exec(
			"INSERT OR IGNORE INTO chats (id, type, title, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
			message.Chat.ID, message.Chat.Type, message.Chat.Title, message.Chat.Username,
			message.Chat.FirstName, message.Chat.LastName, string(chatData))
		if err != nil {
			log.Printf("Error saving chat: %v", err)
		}

		// Save message
		msgData, _ := json.Marshal(message)
		_, err = database.DB.Exec(
			`INSERT INTO messages (
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
			log.Printf("Error saving message: %v", err)
		}

		// Save message entities
		for _, entity := range message.Entities {
			_, err = database.DB.Exec(
				`INSERT INTO message_entities (
					message_id, chat_id, type, offset, length, url, user_id, language, is_caption
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				message.ID, message.Chat.ID, entity.Type, entity.Offset, entity.Length,
				entity.URL, getUserID(entity.User), "", false)
			if err != nil {
				log.Printf("Error saving message entity: %v", err)
			}
		}

		// Save media items
		if message.Photo != nil {
			photoData, _ := json.Marshal(message.Photo)
			_, err = database.DB.Exec(
				`INSERT INTO media_items (
					message_id, chat_id, type, file_id, file_unique_id,
					width, height, file_size, data
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				message.ID, message.Chat.ID, "photo", message.Photo.FileID, "",
				message.Photo.Width, message.Photo.Height, message.Photo.FileSize, string(photoData))
			if err != nil {
				log.Printf("Error saving photo: %v", err)
			}
		}

		for _, d := range registry.Plugins {
			if obj, ok := d.(interface {
				Process(*telebot.Message)
			}); ok {
				go obj.Process(message)
			}
		}
	})

	bot.Start()
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
