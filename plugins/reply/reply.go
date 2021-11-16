package reply

import (
	"os/exec"
	"regexp"
	"math/rand"
	"strings"
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

func (p *ReplyPlugin) Process(message *telebot.Message) {
	bot := registry.Bot
	rngInt := rng.Int()

	techExp := regexp.MustCompile(`(?i)^\!ттх$`)
	questionExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*\?$`)
	dotkaExp := regexp.MustCompile(`(?i)^.*(dota|дота|дот((ец)|(к)+(а|у))).*$`)
	majorExp := regexp.MustCompile(`(?i)^.*(товаризч|(товарищ(ь)?)\s+(майор|генерал|старшина|адмирал|капитан)).*$`)
	doExp := regexp.MustCompile(`(?i)^\!do.*$`)
	// highlightedExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*$`)

	switch {
		case techExp.MatchString(message.Text):
			bot.Send(message.Chat,
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

			bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})

		case dotkaExp.MatchString(message.Text):
			if rngInt % 50 == 0 {
				bot.Send(message.Chat, "Щяб в дотку!", &telebot.SendOptions{})
			}

		case majorExp.MatchString(message.Text):
			if rngInt % 50 == 0 {
				bot.Send(message.Chat, "Так точно!", &telebot.SendOptions{ReplyTo: message})
			} else {
				bot.Send(message.Chat, "Я за него.", &telebot.SendOptions{ReplyTo: message})
			}

		case doExp.MatchString(message.Text):
			execMsg := message.Text
			s := strings.SplitN(execMsg, "docker", 1)
			cmd := exec.Command("docker", s...)
			stdout, err := cmd.Output()
			if err != nil {
				bot.Send(message.Chat, "You idiot", &telebot.SendOptions{ReplyTo: message})
				return
			}
			bot.Send(message.Chat, stdout, &telebot.SendOptions{ReplyTo: message})

		// case highlightedExp.MatchString(message.Text):
		// 	bot.Send(message.Chat, "herp derp", nil)
	}
}
