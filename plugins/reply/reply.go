package reply

import (
	"regexp"
	"math/rand"
	"time"

	"github.com/tucnak/telebot"
	"github.com/asdine/storm"

	"github.com/focusshifter/muxgoob/registry"
)

type ReplyPlugin struct {
}

var db *storm.DB
var rng *rand.Rand

func init() {
	registry.RegisterPlugin(&ReplyPlugin{})
}

func (p *ReplyPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func (p *ReplyPlugin) Process(message telebot.Message) {
	bot := registry.Bot
	rngInt := rng.Int()

	techExp := regexp.MustCompile(`(?i)^\!ттх$`)
	questionExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*\?$`)
	dotkaExp := regexp.MustCompile(`(?i)^.*(dota|дота|дот((ец)|(к)+(а|у))).*$`)
	svetExp := regexp.MustCompile(`(?i)^свет$`)
	// highlightedExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*$`)

	switch {
		case techExp.MatchString(message.Text):
			bot.SendMessage(message.Chat,
						"ТТХ: " + registry.Config.ReplyTechLink,
						&telebot.SendOptions{DisableWebPagePreview: true, DisableNotification: true})

		case questionExp.MatchString(message.Text):
			var replyText string

			switch {
				case rngInt % 100 == 0:
					replyText = "Заткнись, пидор"
				case rngInt % 2 == 0:
					replyText = "Да"
				default:
					replyText = "Нет"
			}
			
			bot.SendMessage(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})

		case dotkaExp.MatchString(message.Text):
			if rngInt % 50 == 0 {
				bot.SendMessage(message.Chat, "Щяб в дотку!", &telebot.SendOptions{})
			}
		
		case svetExp.MatchString(message.Text):
			if rngInt % 50 == 0 {
				bot.SendMessage(message.Chat, "далеких планет", &telebot.SendOptions{})
			}

		// case highlightedExp.MatchString(message.Text):	
		// 	bot.SendMessage(message.Chat, "herp derp", nil)
	}
}
