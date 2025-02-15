package twitchstreams

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"time"

	"github.com/nicklaw5/helix"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

type TwitchstreamsPlugin struct {
}

var rng *rand.Rand
var twitchClient *helix.Client
var twitchTokenRefreshTime time.Time

func init() {
	registry.RegisterPlugin(&TwitchstreamsPlugin{})
}

func (p *TwitchstreamsPlugin) Start(interface{}) {
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
		return
	}

	if streamResponse.StatusCode != 200 {
		log.Printf("Error %v", streamResponse.ErrorMessage)
		return
	}

	streams := streamResponse.Data.Streams

	for _, stream := range streams {
		if stream.UserName == "" || stream.Type != "live" {
			continue
		}

		// Check if we've already seen this stream
		var lastStartedAtStr string
		err := database.DB.QueryRow(
			"SELECT started_at FROM helix_streams WHERE user_name = ?",
			stream.UserName).Scan(&lastStartedAtStr)

		var lastStartedAt time.Time
		if err == nil {
			lastStartedAt, err = time.Parse(time.RFC3339, lastStartedAtStr)
			if err != nil {
				log.Printf("Twitch: Error parsing time %v: %v", lastStartedAtStr, err)
			}
		}

		if err == nil {
			newTime := stream.StartedAt.UTC()
			existingTime := lastStartedAt.UTC()
			log.Printf("Twitch: Debug timestamps - Username: %v", stream.UserName)
			log.Printf("Twitch: New time: %v (%v)", newTime, newTime.Format(time.RFC3339))
			log.Printf("Twitch: Existing time: %v (%v)", existingTime, existingTime.Format(time.RFC3339))
			log.Printf("Twitch: Equal?: %v", newTime.Equal(existingTime))
			if newTime.Equal(existingTime) {
				log.Printf("Twitch: Skipping announcement - same start time")
				continue
			}
		} else {
			log.Printf("Twitch: No existing stream found for %v (err: %v)", stream.UserName, err)
		}

		// Save new stream using INSERT OR REPLACE to handle UNIQUE constraint
		streamData, _ := json.Marshal(stream)
		startedAt := stream.StartedAt.UTC().Format(time.RFC3339)
		_, err = database.DB.Exec(
			"INSERT OR REPLACE INTO helix_streams (user_name, started_at, data) VALUES (?, ?, ?)",
			stream.UserName, startedAt, string(streamData))
		if err != nil {
			log.Printf("Error saving stream: %v", err)
			continue
		}

		log.Printf("Twitch: Announcing %s", stream.UserName)

		game := getGame(stream.GameID)

		messageText := fmt.Sprintf(
			"*%s is playing %s*\nhttps://www.twitch.tv/%s\n%s",
			stream.UserName,
			game.Name,
			stream.UserName,
			stream.Title)

		// Get all chats
		rows, err := database.DB.Query("SELECT id, data FROM chats")
		if err != nil {
			log.Printf("Error getting chats: %v", err)
			continue
		}
		defer rows.Close()

		for rows.Next() {
			var id int64
			var chatData string
			if err := rows.Scan(&id, &chatData); err != nil {
				log.Printf("Error scanning chat: %v", err)
				continue
			}

			var chat telebot.Chat
			if err := json.Unmarshal([]byte(chatData), &chat); err != nil {
				log.Printf("Error unmarshaling chat: %v", err)
				continue
			}

			bot.Send(&chat, messageText, &telebot.SendOptions{
				ParseMode: "markdown",
			})
		}
	}
}

func getGame(gameID string) helix.Game {
	game := helix.Game{ID: "unknown", Name: "Unknown game"}

	// Try to get game from database
	var gameData string
	err := database.DB.QueryRow(
		"SELECT data FROM helix_games WHERE id = ?",
		gameID).Scan(&gameData)

	if err == nil {
		if err := json.Unmarshal([]byte(gameData), &game); err != nil {
			log.Printf("Error unmarshaling game: %v", err)
			return game
		}
		return game
	}

	// Game not found, fetch from Twitch
	gamesResponse, err := twitchClient.GetGames(&helix.GamesParams{
		IDs: []string{gameID},
	})

	if err == nil && len(gamesResponse.Data.Games) > 0 {
		game = gamesResponse.Data.Games[0]

		// Save game to database
		gameData, _ := json.Marshal(game)
		_, err = database.DB.Exec(
			"INSERT INTO helix_games (id, data) VALUES (?, ?)",
			game.ID, string(gameData))
		if err != nil {
			log.Printf("Error saving game: %v", err)
		}
	} else {
		log.Printf("Twitch: Error getting twitch games: %v", err)
	}

	return game
}

func doEvery(d time.Duration, f func(time.Time)) {
	for x := range time.Tick(d) {
		f(x)
	}
}
