package twitchstreams

import (
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"time"

	"github.com/asdine/storm"
	"github.com/nicklaw5/helix"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

type TwitchstreamsPlugin struct {
}

var rng *rand.Rand
var twitchClient *helix.Client
var twitchTokenRefreshTime time.Time
var db *storm.DB

func init() {
	registry.RegisterPlugin(&TwitchstreamsPlugin{})
}

func (p *TwitchstreamsPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
	twitchClient, _ = helix.NewClient(&helix.Options{ClientID: registry.Config.TwitchAPIKey, ClientSecret: registry.Config.TwitchAPISecret})

	twitchTokenRefreshTime = time.Now()

	doEvery(10*time.Second, checkStreams)
}

func (p *TwitchstreamsPlugin) Process(message *telebot.Message) {
	bot := registry.Bot

	streamsExp := regexp.MustCompile(`(?i)^\!(стрим|стрем|riot)$`)

	switch {
	case streamsExp.MatchString(message.Text):
		bot.Send(message.Chat, "GIFF STREM OR RIOT (ノಠ益ಠ)ノ彡┻━┻", &telebot.SendOptions{})
	}
}

func checkAppAccessToken() {
	if twitchTokenRefreshTime.Unix() > time.Now().Unix() {
		return
	}

	log.Printf("Twitch: Setting app access token")

	token, err := twitchClient.GetAppAccessToken()

	if err != nil {
		log.Printf("Twitch: Error getting user token: %v", err)
	}

	twitchTokenRefreshTime = time.Now().Local().Add(time.Second * time.Duration(token.Data.ExpiresIn))

	twitchClient.SetAppAccessToken(token.Data.AccessToken)
}

func checkStreams(t time.Time) {
	checkAppAccessToken()

	log.Printf("Twitch: Checking streams")

	bot := registry.Bot

	streamResponse, err := twitchClient.GetStreams(&helix.StreamsParams{
		UserLogins: registry.Config.TwitchStreams,
	})

	if err != nil {
		log.Printf("Twitch: Error getting twitch streams: %v", err)
	}

	if streamResponse.StatusCode != 200 {
		log.Printf("Error %v", streamResponse.ErrorMessage)
	}

	prevStreams := db.From("helix_streams")
	streams := streamResponse.Data.Streams

	for _, stream := range streams {
		if stream.UserName == "" || stream.Type != "live" {
			continue
		}

		var lastStream helix.Stream
		err := prevStreams.One("UserName", stream.UserName, &lastStream)

		if err == nil {
			log.Printf("Twitch: Comparing %v = %v  %v = %v", stream.UserName, lastStream.UserName, stream.StartedAt, lastStream.StartedAt)
			if stream.StartedAt == lastStream.StartedAt {
				continue
			}
			prevStreams.DeleteStruct(&lastStream)
		}

		prevStreams.Save(&stream)

		log.Printf("Twitch: Announcing %s", stream.UserName)

		game := getGame(stream.GameID)

		messageText := fmt.Sprintf(
			"*%s is playing %s*\nhttps://www.twitch.tv/%s\n%s",
			stream.UserName,
			game.Name,
			stream.UserName,
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

func getGame(gameID string) helix.Game {
	games := db.From("helix_games")

	game := helix.Game{ID: "unknown", Name: "Unknown game"}

	err := games.One("ID", gameID, &game)

	if err != nil {
		gamesResponse, err := twitchClient.GetGames(&helix.GamesParams{
			IDs: []string{gameID},
		})

		if err == nil {
			retrievedGames := gamesResponse.Data.Games

			game = retrievedGames[0]

			games.Save(&game)
		} else {
			log.Printf("Twitch: Error getting twitch games: %v", err)
		}
	}
	return game
}

func doEvery(d time.Duration, f func(time.Time)) {
	for x := range time.Tick(d) {
		f(x)
	}
}
