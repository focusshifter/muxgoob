package nametrigger

import (
	"math/rand"
	"time"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

type NametriggerPlugin struct {
}

var db *storm.DB
var rng *rand.Rand

func init() {
	registry.RegisterPlugin(&NametriggerPlugin{})
}

func (p *NametriggerPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func (p *NametriggerPlugin) Process(message *telebot.Message) {
	bot := registry.Bot
	rngInt := rng.Int()

	for _, trigger := range registry.Config.NametriggerConfig.Triggers {
		for _, username := range trigger.Usernames {
			if username == message.Sender.Username && rngInt%trigger.Chance == 0 {
				bot.Send(message.Chat, trigger.Reply, &telebot.SendOptions{})
			}
		}
	}
}
