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
)

var token string

func main() {
	log.Println("Rise and shine, Mux")

	token = os.Getenv("MUXGOOB_KEY")

	db, err := storm.Open("db/muxgoob.db")
	defer db.Close()

	bot, err := telebot.NewBot(token)
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
				Run(telebot.Message)
			}); ok {
				go obj.Run(message)
			}
		}
	}
}
