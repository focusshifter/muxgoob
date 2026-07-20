package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"

	"github.com/focusshifter/muxgoob/database"
	"github.com/tucnak/telebot"
)

// BotWrapper wraps telebot.Bot to add message saving functionality
type BotWrapper struct {
	*telebot.Bot
	SendFunc     func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error)
	ReplyFunc    func(message *telebot.Message, what interface{}, options ...interface{}) (*telebot.Message, error)
	NotifyFunc   func(to telebot.Recipient, action telebot.ChatAction) error
	SendPollFunc func(to telebot.Recipient, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) (*telebot.Message, error)
	ReactFunc    func(message *telebot.Message, emoji string) error
}

const telegramMessageChunkSize = 4000

type telegramAPIResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

// React adds one emoji reaction to a message. Telegram may reject emojis that
// the chat does not allow; callers treat that as best-effort.
func (b *BotWrapper) React(message *telebot.Message, emoji string) error {
	if b == nil || message == nil || message.Chat == nil {
		return errors.New("reaction requires a message with a chat")
	}
	if b.ReactFunc != nil {
		return b.ReactFunc(message, emoji)
	}
	if b.Bot == nil {
		return errors.New("bot is not initialized")
	}
	response, err := b.Bot.Raw("setMessageReaction", map[string]interface{}{
		"chat_id":    message.Chat.ID,
		"message_id": message.ID,
		"reaction": []map[string]string{{
			"type":  "emoji",
			"emoji": emoji,
		}},
	})
	if err != nil {
		return err
	}
	var result telegramAPIResponse
	if err := json.Unmarshal(response, &result); err != nil {
		return fmt.Errorf("decode setMessageReaction response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("setMessageReaction failed: %s", result.Description)
	}
	return nil
}

// Send sends a message and saves it to the database.
// String payloads longer than Telegram's limit are split into multiple messages.
func (b *BotWrapper) Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
	chunks := splitOutgoingMessage(what)
	if len(chunks) == 0 {
		chunks = []interface{}{what}
	}

	var lastMsg *telebot.Message
	for _, chunk := range chunks {
		msg, err := b.sendSingle(to, chunk, options...)
		if err != nil {
			return msg, err
		}
		lastMsg = msg
	}

	return lastMsg, nil
}

func (b *BotWrapper) sendSingle(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
	// If we have a custom SendFunc (for testing), use it
	if b.SendFunc != nil {
		return b.SendFunc(to, what, options...)
	}

	// Otherwise use the real bot
	msg, err := b.Bot.Send(to, what, options...)
	if err != nil {
		return msg, err
	}

	// Save bot's message to database
	if msg != nil {
		err = database.RetryWithBackoff(func() error {
			return database.WithTx(context.Background(), func(tx *sql.Tx) error {
				// Save bot user if not exists
				userData, _ := json.Marshal(msg.Sender)
				_, err := tx.Exec(
					"INSERT OR IGNORE INTO users (id, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?)",
					msg.Sender.ID, msg.Sender.Username, msg.Sender.FirstName, msg.Sender.LastName, string(userData))
				if err != nil {
					return err
				}

				// Save chat if not exists
				chatData, _ := json.Marshal(msg.Chat)
				_, err = tx.Exec(
					"INSERT OR IGNORE INTO chats (id, type, title, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?, ?, ?)",
					msg.Chat.ID, msg.Chat.Type, msg.Chat.Title, msg.Chat.Username,
					msg.Chat.FirstName, msg.Chat.LastName, string(chatData))
				if err != nil {
					return err
				}

				// Save message
				msgData, _ := json.Marshal(msg)
				_, err = tx.Exec(
					`INSERT INTO messages (
						id, chat_id, sender_id, reply_to_message_id, forward_from_id,
						forward_from_chat_id, forward_date, edit_date, media_group_id,
						author_signature, unixtime, text, caption, data
					) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					msg.ID, msg.Chat.ID, msg.Sender.ID,
					getMessageID(msg.ReplyTo), getUserID(msg.OriginalSender),
					getChatID(msg.OriginalChat), msg.OriginalUnixtime, msg.LastEdit,
					msg.AlbumID, msg.Signature, msg.Time().Unix(),
					msg.Text, msg.Caption, string(msgData))
				if err != nil {
					return err
				}

				// Save message entities
				for _, entity := range msg.Entities {
					_, err = tx.Exec(
						`INSERT INTO message_entities (
							message_id, chat_id, type, offset, length, url, user_id, language, is_caption
						) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
						msg.ID, msg.Chat.ID, entity.Type, entity.Offset, entity.Length,
						entity.URL, getUserID(entity.User), "", false)
					if err != nil {
						return err
					}
				}
				return nil
			})
		})

		if err != nil {
			log.Printf("Error saving message data: %v", err)
		}
	}

	return msg, err
}

func splitOutgoingMessage(what interface{}) []interface{} {
	text, ok := what.(string)
	if !ok {
		return []interface{}{what}
	}

	chunks := splitMessage(text, telegramMessageChunkSize)
	result := make([]interface{}, 0, len(chunks))
	for _, chunk := range chunks {
		result = append(result, chunk)
	}
	return result
}

func splitMessage(text string, limit int) []string {
	if text == "" {
		return []string{""}
	}

	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}

	var chunks []string
	for len(runes) > limit {
		splitAt := limit
		for i := limit; i > limit-200 && i > 0; i-- {
			if runes[i-1] == '\n' || runes[i-1] == ' ' {
				splitAt = i
				break
			}
		}

		chunks = append(chunks, string(runes[:splitAt]))
		runes = runes[splitAt:]
	}

	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}

	return chunks
}

// Helper functions copied from main.go
// Reply sends a reply to a message and saves it to the database.
// String payloads are split through Send so long replies also respect Telegram limits.
func (b *BotWrapper) Reply(message *telebot.Message, what interface{}, options ...interface{}) (*telebot.Message, error) {
	// If we have a custom ReplyFunc (for testing), use it
	if b.ReplyFunc != nil {
		return b.ReplyFunc(message, what, options...)
	}

	if message == nil || message.Chat == nil {
		return nil, errors.New("reply message and chat are required")
	}

	forwardedOptions := make([]interface{}, len(options))
	copy(forwardedOptions, options)

	hasSendOptions := false
	for i, option := range forwardedOptions {
		if opt, ok := option.(*telebot.SendOptions); ok && opt != nil {
			copied := *opt
			if copied.ReplyTo == nil {
				copied.ReplyTo = message
			}
			forwardedOptions[i] = &copied
			hasSendOptions = true
		}
	}

	if !hasSendOptions {
		forwardedOptions = append(forwardedOptions, &telebot.SendOptions{ReplyTo: message})
	}

	return b.Send(message.Chat, what, forwardedOptions...)
}

func getMessageID(msg *telebot.Message) interface{} {
	if msg == nil {
		return nil
	}
	return msg.ID
}

func getUserID(user *telebot.User) interface{} {
	if user == nil {
		return nil
	}
	return user.ID
}

func getChatID(chat *telebot.Chat) interface{} {
	if chat == nil {
		return nil
	}
	return chat.ID
}

// Notify sends a chat action notification
func (b *BotWrapper) Notify(to telebot.Recipient, action telebot.ChatAction) error {
	// If we have a custom NotifyFunc (for testing), use it
	if b.NotifyFunc != nil {
		return b.NotifyFunc(to, action)
	}

	// Otherwise use the real bot
	if b.Bot != nil {
		return b.Bot.Notify(to, action)
	}

	// If no bot is available, return nil
	return nil
}

func (b *BotWrapper) SendPoll(to telebot.Recipient, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) (*telebot.Message, error) {
	if b.SendPollFunc != nil {
		return b.SendPollFunc(to, question, options, isAnonymous, allowsMultipleAnswers)
	}

	if b.Bot == nil {
		return nil, errors.New("bot is not initialized")
	}

	encodedOptions, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("marshal poll options: %w", err)
	}

	params := map[string]string{
		"chat_id":                 to.Recipient(),
		"question":                question,
		"options":                 string(encodedOptions),
		"is_anonymous":            strconv.FormatBool(isAnonymous),
		"allows_multiple_answers": strconv.FormatBool(allowsMultipleAnswers),
	}

	respJSON, err := b.Bot.Raw("sendPoll", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Ok          bool             `json:"ok"`
		Result      *telebot.Message `json:"result"`
		Description string           `json:"description"`
	}
	if err := json.Unmarshal(respJSON, &resp); err != nil {
		return nil, fmt.Errorf("bad response json: %w", err)
	}
	if !resp.Ok {
		return nil, fmt.Errorf("api error: %s", resp.Description)
	}

	return resp.Result, nil
}
