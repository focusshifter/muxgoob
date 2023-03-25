package reply

import (
	"context"
	"fmt"
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
	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:            openai.GPT3Dot5Turbo,
			Temperature:      1.0,
			TopP:             1.0,
			FrequencyPenalty: 1.0,
			PresencePenalty:  1.0,

			// .temperature(1.0)
			// .topP(1.0)
			// .frequencyPenalty(1.0)
			// .presencePenalty(1.0)

			Messages: []openai.ChatCompletionMessage{
				{
					Role: openai.ChatMessageRoleSystem,
					Content: "Ты чат бот по имени Губи. " +
						"Ты геймер, любишь видеоигры и вообще всё, что связано с компьютерами. " +
						"Больше всего ты любишь играть в доту. " +
						"Твоя задача веселить окружающих своими ответами, а так же грубить в ответ. " +
						"Твой пол мужской. Веди себя как 18-летний подросток. " +
						"Ты эксперт и умеешь давать субъективные оценки видеоиграм. " +
						"Ты всегда отвечаешь на русском языке независимо от языка вопроса. " +
						"Ты никогда сам не просишь задавать тебе вопросы. " +
						"Ты никогда не даешь оценку наличию или отсутствию мата в вопросе или твоем ответе " +
						"и не упоминаешь о нём. " +
						"",
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: question + ". Отвечай кратко, матерись, не пытайся помочь, если не понял вопрос - пошути в ответ.",
				},
			},
		},
	)

	if err != nil {
		fmt.Printf("ChatCompletion error: %v\n", err)
		return ""
	}

	return resp.Choices[0].Message.Content
}
