package Name

import (
	"math/rand"
	"regexp"
	"time"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

type NamePlugin struct {
}

var db *storm.DB
var rng *rand.Rand

func init() {
	registry.RegisterPlugin(&NamePlugin{})
}

func (p *NamePlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func (p *NamePlugin) Process(message telebot.Message) {
	bot := registry.Bot
	rngInt := rng.Int()

	usernameExp := regexp.MustCompile(registry.Config.UkrainianUsernames)

	switch {
	case usernameExp.MatchString(message.Sender.Username):
		if rngInt%50 == 0 {
			bot.SendMessage(message.Chat, registry.Config.ReplyUkrainians, &telebot.SendOptions{})
		}

	}
}
