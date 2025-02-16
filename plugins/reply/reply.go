package reply

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"regexp"
	"sort"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

type ReplyPlugin struct {
}

var sqliteDb *sql.DB
var rng *rand.Rand

func init() {
	registry.RegisterPlugin(&ReplyPlugin{})
}

func (p *ReplyPlugin) Start(_ interface{}) {
	var err error
	sqliteDb, err = sql.Open("sqlite3", "db/muxgoob.sqlite")
	if err != nil {
		log.Fatal("Failed to open SQLite DB:", err)
	}
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func (p *ReplyPlugin) Process(message *telebot.Message) {
	bot := registry.Bot
	rngInt := rng.Int()

	// Check if this is a reply to bot's message
	if message.ReplyTo != nil && message.ReplyTo.Sender.Username == bot.Me.Username {
		replyText := askChatGpt(message)
		if replyText != "" {
			bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
		}
		return
	}

	techExp := regexp.MustCompile(`(?i)^\!ттх$`)
	questionExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*\?$`)
	commandExp := regexp.MustCompile(`(?i)^(gooby|губи|губ(я)+н),.*$`)
	dotkaExp := regexp.MustCompile(`(?i)^.*(dota|дота|дот((ец)|(к)+(а|у))).*$`)
	majorExp := regexp.MustCompile(`(?i)^.*(товаризч|(товарищ(ь)?)\s+(майор|генерал|старшина|адмирал|капитан)).*$`)
	// highlightedExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*$`)

	switch {
	case techExp.MatchString(message.Text):
		bot.Send(message.Chat,
			"ТТХ: "+registry.Config.ReplyTechLink,
			&telebot.SendOptions{DisableWebPagePreview: true, DisableNotification: true})

	case questionExp.MatchString(message.Text):
		replyText := askChatGpt(message)

		if replyText == "" {
			switch {
			case rngInt%100 == 0:
				replyText = "Заткнись, пидор"
			case rngInt%2 == 0:
				replyText = "Да"
			default:
				replyText = "Нет"
			}
		}

		bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})

	case commandExp.MatchString(message.Text):
		replyText := askChatGpt(message)

		if replyText != "" {
			bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
		}

	case dotkaExp.MatchString(message.Text):
		if rngInt%50 == 0 {
			bot.Send(message.Chat, "Щяб в дотку!", &telebot.SendOptions{})
		}

	case majorExp.MatchString(message.Text):
		if rngInt%50 == 0 {
			bot.Send(message.Chat, "Так точно!", &telebot.SendOptions{ReplyTo: message})
		} else {
			bot.Send(message.Chat, "Я за него.", &telebot.SendOptions{ReplyTo: message})
		}

		// case highlightedExp.MatchString(message.Text):
		// 	bot.Send(message.Chat, "herp derp", nil)

	default:
		if rngInt%100 == 0 && len(message.Text) > 150 {
			replyText := askChatGpt(message)

			if replyText != "" {
				bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
			}
		}
	}
}

func retrieveHistoryForChat(chatID int64, messageCount int) []telebot.Message {
	rows, err := sqliteDb.Query(
		`SELECT data FROM messages 
		WHERE chat_id = ? 
		ORDER BY unixtime DESC LIMIT ?`,
		chatID, messageCount)
	if err != nil {
		log.Printf("Error retrieving chat history: %v", err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			log.Printf("Error scanning message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	// Sort by ID for consistent order
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Unixtime < messages[j].Unixtime
	})

	log.Printf("Retrieved %v messages", len(messages))

	return messages
}

func generateChatGptHistory(messages []telebot.Message) string {
	var history string
	var username string

	for _, message := range messages {
		if message.Sender.Username != "" {
			username = message.Sender.Username
		} else {
			username = message.Sender.FirstName + " " + message.Sender.LastName
		}
		history += fmt.Sprintf("%s: %s\n", username, message.Text)
	}

	return history
}

func askChatGpt(message *telebot.Message) string {
	question := message.Text

	client := openai.NewClient(registry.Config.OpenaiApiKey)

	systemMessage := registry.Config.ChatGptSystemPrompt

	userMessage := fmt.Sprintf(registry.Config.ChatGptUserPrompt, question)

	model := openai.GPT4o

	log.Printf("ChatGPT request: model %v", model)
	log.Printf("ChatGPT request: chat_id %v", message.Chat.ID)
	log.Printf("ChatGPT request: system %v", systemMessage)
	log.Printf("ChatGPT request: user %v", userMessage)

	if registry.Config.ChatGptUseHistory {
		history := generateChatGptHistory(retrieveHistoryForChat(message.Chat.ID, registry.Config.ChatGptHistoryDepth))

		log.Printf("ChatGPT request: history %v", history)

		systemMessage += "\n\nВ чате произошел следующий диалог: \n" + history
	}

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:            model,
			Temperature:      0.7,
			TopP:             1.0,
			FrequencyPenalty: 0.2,
			PresencePenalty:  0.2,

			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemMessage,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userMessage,
				},
			},
		},
	)

	if err != nil {
		log.Printf("ChatCompletion error: %v", err)
		return ""
	}

	return resp.Choices[0].Message.Content
}
