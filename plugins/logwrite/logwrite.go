package logwrite

import (
	"log"
	"strconv"

	"github.com/tucnak/telebot"
	"github.com/asdine/storm"

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
	log.Println("Message saved")
	chat := db.From(strconv.FormatInt(message.Chat.ID, 10))
	chat.Save(&message)
}
