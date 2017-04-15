package reply

import (
	"regexp"

	"github.com/tucnak/telebot"
	"github.com/asdine/storm"

	"github.com/focusshifter/muxgoob/registry"
)

type ReplyPlugin struct {
}

var db *storm.DB

func init() {
	registry.RegisterPlugin(&ReplyPlugin{})
}

func (p *ReplyPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
}

func (p *ReplyPlugin) Run(message telebot.Message) {
	highlightedExp := regexp.MustCompile(`^.*(gooby|губи|губ(я)+н).*$`)

	if highlightedExp.MatchString(message.Text) {
		bot := registry.Bot

		bot.SendMessage(message.Chat, "herp derp", nil)
	}
}

func (p *ReplyPlugin) Stop() {
}
