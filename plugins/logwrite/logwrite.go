package logwrite

import (
	"log"
	"strconv"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

type LogWritePlugin struct {
}

var db *storm.DB

func init() {
	registry.RegisterPlugin(&LogWritePlugin{})
}

func (p *LogWritePlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
}

func (p *LogWritePlugin) Process(message *telebot.Message) {
	chat := db.From(strconv.FormatInt(message.Chat.ID, 10))

	log.Println("Message save, chat ID:", strconv.FormatInt(message.Chat.ID, 10))

	err := chat.Save(message)

	if err != nil {
		log.Println("Error saving message:", err)
	}

	chats := db.From("chats")

	var existingChat telebot.Chat
	err = chats.One("ID", message.Chat.ID, &existingChat)

	if err != nil {
		chats.Save(message.Chat)
		log.Println("Chat list updated, new chat ID:", message.Chat.ID)
	}
}
