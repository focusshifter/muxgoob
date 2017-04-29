package main

import (
	"time"
	"log"
	"os"
	
	"github.com/tucnak/telebot"
	"github.com/asdine/storm"

	"github.com/focusshifter/muxgoob/registry"

	_ "github.com/focusshifter/muxgoob/plugins/reply"
	_ "github.com/focusshifter/muxgoob/plugins/logwrite"
	_ "github.com/focusshifter/muxgoob/plugins/dupelink"
	_ "github.com/focusshifter/muxgoob/plugins/nametrigger"
	_ "github.com/focusshifter/muxgoob/plugins/birthdays"
)

var token string

func main() {
	log.Println("Rise and shine, Mux")

	token = os.Getenv("MUXGOOB_KEY")

	registry.LoadConfig("config.yml")

	db, err := storm.Open("db/muxgoob.db")
	defer db.Close()

	bot, err := telebot.NewBot(registry.Config.TelegramKey)
	if err != nil {
		log.Fatal(err)
	}

	registry.Bot = bot

	for _, d := range registry.Plugins {
		go d.Start(db)
	}

	messages := make(chan telebot.Message)
	bot.Listen(messages, 1*time.Second)

	for message := range messages {
		for _, d := range registry.Plugins {
			if obj, ok := d.(interface {
				Process(telebot.Message)
			}); ok {
				go obj.Process(message)
			}
		}
	}
}
