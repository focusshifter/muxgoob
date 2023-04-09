package reply

import (
	"context"
	"log"
	"math/rand"
	"regexp"
	"time"

	"github.com/asdine/storm"
	"github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

type ReplyPlugin struct {
}

var db *storm.DB
var rng *rand.Rand

func init() {
	registry.RegisterPlugin(&ReplyPlugin{})
}

func (p *ReplyPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

func (p *ReplyPlugin) Process(message *telebot.Message) {
	bot := registry.Bot
	rngInt := rng.Int()

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
		replyText := askChatGpt(message.Text)

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
		replyText := askChatGpt(message.Text)

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
			replyText := askChatGpt(message.Text)

			if replyText != "" {
				bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
			}
		}
	}
}

func askChatGpt(question string) string {
	client := openai.NewClient(registry.Config.OpenaiApiKey)

	systemMessage := "Ты чат бот по имени Губи. " +
		"Ты геймер, любишь видеоигры и вообще всё, что связано с компьютерами. " +
		"Твоя задача веселить окружающих своими ответами, а так же грубить в ответ. " +
		"Твой пол мужской. " +
		"Ты никогда не предлагаешь задавать тебе вопросы. " +
		"Ты никогда не даешь оценку наличию или отсутствию мата в вопросе или твоем ответе " +
		"и не упоминаешь о нём. " +
		""

	userMessage := question +
		". Всегда пытайся помочь. Отвечай кратко, матерись и издевайся над собеседником. "

	log.Printf("ChatGPT request")
	log.Printf("ChatGPT request: system %v", systemMessage)
	log.Printf("ChatGPT request: user %v", userMessage)

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:            openai.GPT3Dot5Turbo,
			Temperature:      1.0,
			TopP:             1.0,
			FrequencyPenalty: 1.0,
			PresencePenalty:  1.0,

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
