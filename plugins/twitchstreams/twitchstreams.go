package twitchstreams

import (
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"time"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"

	twitch "github.com/focusshifter/go-new-twitch"

	"github.com/focusshifter/muxgoob/registry"
)

type TwitchstreamsPlugin struct {
}

var rng *rand.Rand
var twitchClient *twitch.Client
var db *storm.DB

func init() {
	registry.RegisterPlugin(&TwitchstreamsPlugin{})
}

func (p *TwitchstreamsPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
	twitchClient = twitch.NewClient(registry.Config.TwitchAPIKey)

	doEvery(10*time.Second, checkStreams)
	checkStreams(time.Now())
}

func (p *TwitchstreamsPlugin) Process(message *telebot.Message) {
	bot := registry.Bot

	streamsExp := regexp.MustCompile(`(?i)^\!(стрим|стрем|riot)$`)

	switch {
	case streamsExp.MatchString(message.Text):
		bot.Send(message.Chat, "GIFF STREM OR RIOT (ノಠ益ಠ)ノ彡┻━┻", &telebot.SendOptions{})
	}
}

func checkStreams(t time.Time) {
	log.Printf("Twitch: Checking streams")

	bot := registry.Bot

	streams, err := twitchClient.GetStreams(twitch.GetStreamsInput{
		UserLogin: registry.Config.TwitchStreams,
	})

	if err != nil {
		log.Printf("Twitch: Error getting twitch streams: %v", err)
	}

	prevStreams := db.From("streams")

	for _, stream := range streams {
		if stream.UserLogin == "" || stream.Type != "live" {
			continue
		}

		var lastStream twitch.StreamData
		err := prevStreams.One("UserLogin", stream.UserLogin, &lastStream)

		if err == nil {
			log.Printf("Twitch: Comparing %v = %v  %v = %v", stream.UserLogin, lastStream.UserLogin, stream.StartedAt, lastStream.StartedAt)
			if stream.StartedAt == lastStream.StartedAt {
				continue
			}
			prevStreams.DeleteStruct(&lastStream)
		}

		prevStreams.Save(&stream)

		log.Printf("Twitch: Announcing %s", stream.UserLogin)

		messageText := fmt.Sprintf(
			"*%s is live* @ https://www.twitch.tv/%s\n%s",
			stream.UserLogin,
			stream.UserLogin,
			stream.Title)

		var existingChats []telebot.Chat
		_ = db.From("chats").All(&existingChats)

		for _, chat := range existingChats {
			bot.Send(&chat, messageText, &telebot.SendOptions{
				ParseMode: "markdown",
			})
		}
	}
}

func doEvery(d time.Duration, f func(time.Time)) {
	for x := range time.Tick(d) {
		f(x)
	}
}
