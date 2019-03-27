package main

import (
	"log"
	"os"
	"time"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"

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

	db, err := storm.Open("db/muxgoob.db")
	defer db.Close()

	bot, err := telebot.NewBot(telebot.Settings{
		Token:  registry.Config.TelegramKey,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Fatal(err)
	}

	registry.Bot = bot

	for _, d := range registry.Plugins {
		go d.Start(db)
	}

	bot.Handle(telebot.OnText, func(message *telebot.Message) {
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
