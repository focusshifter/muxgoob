package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"

	_ "github.com/focusshifter/muxgoob/plugins/admin"
	_ "github.com/focusshifter/muxgoob/plugins/birthdays"
	_ "github.com/focusshifter/muxgoob/plugins/cron"
	_ "github.com/focusshifter/muxgoob/plugins/dupelink"
	_ "github.com/focusshifter/muxgoob/plugins/logwrite"
	_ "github.com/focusshifter/muxgoob/plugins/nametrigger"
	_ "github.com/focusshifter/muxgoob/plugins/promptmgr"
	_ "github.com/focusshifter/muxgoob/plugins/reply"
	_ "github.com/focusshifter/muxgoob/plugins/selfprompt"
	_ "github.com/focusshifter/muxgoob/plugins/spotify"
	_ "github.com/focusshifter/muxgoob/plugins/twitchstreams"
	_ "github.com/focusshifter/muxgoob/plugins/version"
)

var token string

const gracefulShutdownTimeout = 10 * time.Minute

func main() {
	log.Println("[muxgoob] Rise and shine, Mux")

	token = os.Getenv("MUXGOOB_KEY")

	registry.LoadConfig("config.yml")

	// Initialize databases
	database.Initialize()
	defer database.DB.Close()

	// Initialize database settings in registry
	registry.InitializeDbSettings()

	// Initialize StormDB for legacy support
	stormDb, err := storm.Open("db/muxgoob.db")
	if err != nil {
		log.Fatal("[muxgoob] Failed to open StormDB:", err)
	}
	defer stormDb.Close()

	bot, err := telebot.NewBot(telebot.Settings{
		Token:  registry.Config.TelegramKey,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	})

	if err != nil {
		log.Fatal("[muxgoob] ", err)
	}

	registry.Bot = &registry.BotWrapper{Bot: bot}

	for _, d := range registry.Plugins {
		go d.Start(stormDb)
	}

	bot.Handle(telebot.OnText, handleIncomingMessage)
	bot.Handle(telebot.OnPhoto, handleIncomingMessage)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		sig := <-signals
		log.Printf("[muxgoob] Received %s; stopping Telegram polling and draining work", sig)
		bot.Stop()
	}()

	bot.Start()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), gracefulShutdownTimeout)
	defer cancel()
	registry.Shutdown(shutdownCtx)
	log.Printf("[muxgoob] Graceful shutdown complete")
}

func handleIncomingMessage(message *telebot.Message) {
	if message == nil || message.Chat == nil || message.Sender == nil || database.DB == nil {
		return
	}
	changed, err := database.SaveIncomingMessage(context.Background(), database.DB, message)
	if err != nil {
		log.Printf("[muxgoob] Error ingesting chat=%d message=%d: %v", message.Chat.ID, message.ID, err)
		return
	}
	if !changed {
		return
	}

	registry.DispatchMessage(message)
}
