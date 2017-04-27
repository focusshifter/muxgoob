package Name

import (
  "regexp"
  "math/rand"
  "time"

  "github.com/tucnak/telebot"
  "github.com/asdine/storm"

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

	hohlyExp := regexp.MustCompile(`yuriyglebov|dbobylev.*$`)

	switch {
		case hohlyExp.MatchString(message.Sender.Username):
		bot.SendMessage(message.Chat, "ЖИЛИ У БАБУСИ ДВА ВЕСЕЛЫХ ГУСЯ, ОДИН ЖОВТЫЙ ДРУГОЙ СИНИЙ, СЛАВА УКРАИНИ", &telebot.SendOptions{})
			}	

	}