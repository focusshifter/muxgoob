package logwrite

import (
	"log"
	"strconv"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

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
	// SQLite persistence is owned by the atomic ingress path in muxgoob.go.
	// This plugin remains only as a compatibility writer for legacy Storm data.
	if message == nil || message.Chat == nil || p.stormDb == nil {
		return
	}
	chat := p.stormDb.From(strconv.FormatInt(message.Chat.ID, 10))
	if err := chat.Save(message); err != nil {
		log.Printf("[logwrite] Error saving message to Storm: %v", err)
	}
	chats := p.stormDb.From("chats")
	var existingChat telebot.Chat
	if err := chats.One("ID", message.Chat.ID, &existingChat); err != nil {
		if err := chats.Save(message.Chat); err != nil {
			log.Printf("[logwrite] Error saving chat to Storm: %v", err)
		}
		log.Printf("[logwrite] Chat list updated in Storm, new chat ID: %d", message.Chat.ID)
	}
}
