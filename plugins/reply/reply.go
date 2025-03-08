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

	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
)

// RandomGenerator defines an interface for random number generation
type RandomGenerator interface {
	// Intn returns a random int in [0,n)
	Intn(n int) int
}

// RealRandomGenerator implements RandomGenerator using the standard library
type RealRandomGenerator struct {
	rng *rand.Rand
}

func NewRealRandomGenerator() *RealRandomGenerator {
	return &RealRandomGenerator{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (r *RealRandomGenerator) Intn(n int) int {
	return r.rng.Intn(n)
}

// ChatGptClient defines an interface for interacting with ChatGPT
type ChatGptClient interface {
	Ask(message *telebot.Message) string
}

// RealChatGptClient implements ChatGptClient using the actual OpenAI API
type RealChatGptClient struct{}

func (c *RealChatGptClient) Ask(message *telebot.Message) string {
	// The actual implementation will be moved from askChatGpt
	return askChatGpt(message)
}

type ReplyPlugin struct {
	random     RandomGenerator
	chatClient ChatGptClient
}

var sqliteDb *sql.DB

func init() {
	// Register with default implementations for production
	plugin := &ReplyPlugin{
		random:     NewRealRandomGenerator(),
		chatClient: &RealChatGptClient{},
	}
	registry.RegisterPlugin(plugin)
}

func (p *ReplyPlugin) Start(_ interface{}) {
	var err error
	sqliteDb, err = sql.Open("sqlite3", "db/muxgoob.sqlite")
	if err != nil {
		log.Fatal("Failed to open SQLite DB:", err)
	}
}

// SetDependencies allows injecting dependencies for testing
func (p *ReplyPlugin) SetDependencies(random RandomGenerator, chatClient ChatGptClient) {
	p.random = random
	p.chatClient = chatClient
}

func (p *ReplyPlugin) Process(message *telebot.Message) {
	// Safety check for nil message
	if message == nil {
		log.Printf("[reply] Received nil message in Process")
		return
	}

	// Safety check for nil bot
	bot := registry.Bot
	if bot == nil {
		log.Printf("[reply] Bot is nil in Process")
		return
	}

	// Safety check for nil message.Chat
	if message.Chat == nil {
		log.Printf("[reply] Message.Chat is nil in Process")
		return
	}

	// Check if this is a reply to bot's message
	if message.ReplyTo != nil {
		log.Printf("[reply] ReplyTo is not nil")
		if message.ReplyTo.Sender != nil {
			log.Printf("[reply] ReplyTo.Sender is not nil, username: %s", message.ReplyTo.Sender.Username)
			// Safety check for bot
			if bot == nil {
				log.Printf("[reply] Bot is nil")
				return
			}

			if bot.Me == nil {
				log.Printf("[reply] Bot.Me is nil")
				return
			}

			log.Printf("[reply] Bot.Me is not nil, username: %s", bot.Me.Username)
			if message.ReplyTo.Sender.Username == bot.Me.Username {
				// Use the injected chat client
				log.Printf("[reply] Username matches, calling chat client")
				replyText := p.chatClient.Ask(message)
				log.Printf("[reply] Chat client returned: %s", replyText)
				if replyText != "" {
					log.Printf("[reply] Sending reply")
					bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
				}
				return
			} else {
				log.Printf("[reply] Username doesn't match: %s vs %s", message.ReplyTo.Sender.Username, bot.Me.Username)
			}
		} else {
			log.Printf("[reply] ReplyTo.Sender is nil")
		}
	}

	// Define regex patterns based on test mode
	var techExp, questionExp, commandExp, dotkaExp, majorExp *regexp.Regexp

	// Use simplified patterns in test mode for predictable behavior
	techExp = regexp.MustCompile(`(?i)^\!ттх$`)
	questionExp = regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*\?$`)
	commandExp = regexp.MustCompile(`(?i)^(gooby|губи|губ(я)+н),.*$`)
	dotkaExp = regexp.MustCompile(`(?i)^.*(dota|дота|дот((ец)|(к)+(а|у))).*$`)
	majorExp = regexp.MustCompile(`(?i)^.*(товаризч|(товарищ(ь)?)\s+(майор|генерал|старшина|адмирал|капитан)).*$`)
	// highlightedExp := regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н).*$`)

	switch {
	case techExp.MatchString(message.Text):
		bot.Send(message.Chat,
			"ТТХ: "+registry.Config.ReplyTechLink,
			&telebot.SendOptions{DisableWebPagePreview: true, DisableNotification: true})

	case questionExp.MatchString(message.Text):
		replyText := p.chatClient.Ask(message)

		if replyText == "" {
			// Use the injected random generator for consistent behavior in tests
			randomValue := p.random.Intn(100)
			switch {
			case randomValue%2 == 0:
				replyText = "Да"
			default:
				replyText = "Нет"
			}
		}

		bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})

	case commandExp.MatchString(message.Text):
		replyText := p.chatClient.Ask(message)

		if replyText != "" {
			bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
		}

	case dotkaExp.MatchString(message.Text):
		// Use the injected random generator for consistent behavior in tests
		if p.random.Intn(50) == 0 {
			bot.Send(message.Chat, "Щяб в дотку!", &telebot.SendOptions{})
		}

	case majorExp.MatchString(message.Text):
		// Use the injected random generator for consistent behavior in tests
		if p.random.Intn(2) == 0 {
			bot.Send(message.Chat, "Так точно!", &telebot.SendOptions{ReplyTo: message})
		} else {
			bot.Send(message.Chat, "Я за него.", &telebot.SendOptions{ReplyTo: message})
		}

		// case highlightedExp.MatchString(message.Text):
		// 	bot.Send(message.Chat, "herp derp", nil)

	default:
		if p.random.Intn(100) == 0 && len(message.Text) > 150 {
			replyText := p.chatClient.Ask(message)

			if replyText != "" {
				bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
			}
		}
	}
}

func retrieveHistoryForChat(chatID int64, messageCount int) []telebot.Message {
	// Check if database is initialized
	if sqliteDb == nil {
		log.Printf("[reply] Database not initialized")
		return nil
	}

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
			log.Printf("[reply] Error scanning message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("[reply] Error unmarshaling message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	// Sort by ID for consistent order
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Unixtime < messages[j].Unixtime
	})

	log.Printf("[reply] Retrieved %v messages", len(messages))

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

// askChatGpt is a variable function that can be replaced in tests
var askChatGpt = func(message *telebot.Message) string {
	// Safety check for test environment
	if message == nil {
		log.Printf("[reply] Message is nil in askChatGpt")
		return ""
	}

	question := message.Text

	// No need to check if registry.Config is initialized as it's not a pointer type

	var config openai.ClientConfig
	var model string

	if registry.Config.AiProvider == "openrouter" {
		config = openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		model = registry.Config.AiModel
	} else {
		config = openai.DefaultConfig(registry.Config.OpenaiApiKey)
		model = "gpt-4o-mini"
	}

	client := openai.NewClientWithConfig(config)

	// Get prompt from promptmgr
	// Check if message.Chat is nil to prevent nil pointer dereference
	if message.Chat == nil {
		log.Printf("[reply] Message.Chat is nil in askChatGpt")
		return ""
	}

	systemMessage, err := promptmgr.GetCurrentPrompt(message.Chat.ID, true)
	if err != nil {
		log.Printf("[reply] Error getting prompt: %v", err)
		return ""
	}

	userMessage := fmt.Sprintf(registry.Config.ChatGptUserPrompt, question)

	log.Printf("ChatGPT request: model %v", model)
	log.Printf("ChatGPT request: chat_id %v", message.Chat.ID)
	log.Printf("ChatGPT request: system %v", systemMessage)
	log.Printf("ChatGPT request: user %v", userMessage)

	if registry.Config.ChatGptUseHistory {
		// Check if message.Chat is nil to prevent nil pointer dereference
		if message.Chat == nil {
			log.Printf("[reply] Message.Chat is nil when retrieving history")
		} else {
			history := generateChatGptHistory(retrieveHistoryForChat(message.Chat.ID, registry.Config.ChatGptHistoryDepth))

			log.Printf("[reply] ChatGPT request: history %v", history)

			userMessage += "\n\nВ чате произошел следующий диалог: \n" + history
		}
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
