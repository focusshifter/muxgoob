package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

// A deliberately small subset of Telegram's standard emoji reactions. Intent is
// chosen by the existing completion, not by matching the incoming message text.
var replyReactionEmojis = []string{"👍", "❤", "🔥", "🎉", "👏", "😁", "😢", "🤔", "👀", "🤝", "💯"}

type reactionBot interface {
	AvailableReactionEmojis(chatID int64) ([]string, bool, error)
	React(message *telebot.Message, emoji string) error
}

type ReactToMessageTool struct {
	message *telebot.Message
	bot     reactionBot
	sent    bool
}

func NewReactToMessageTool(message *telebot.Message) *ReactToMessageTool {
	tool := &ReactToMessageTool{bot: registry.Bot}
	// Bind the target in host code; the model cannot select a history message,
	// reply parent, or another chat, even through additional JSON properties.
	if message != nil && message.Chat != nil {
		tool.message = &telebot.Message{ID: message.ID, Chat: &telebot.Chat{ID: message.Chat.ID}}
	}
	return tool
}

func (t *ReactToMessageTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "reactToMessage",
			Description: "Optionally respond to the current incoming Telegram message with a single emoji reaction INSTEAD OF text. Prefer this for a simple acknowledgement or obvious emotional response when words add nothing. Never replace a substantive question, task, image/poll request, or needed explanation with a reaction. Use only as the sole reply, not alongside other actions. Success ends the reply immediately with no text; on failure, continue normally without claiming success.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"emoji": map[string]any{"type": "string", "enum": replyReactionEmojis}},
				"required":             []string{"emoji"},
				"additionalProperties": false,
			},
		},
	}
}

func (t *ReactToMessageTool) Execute(ctx context.Context, args string) (string, error) {
	if t == nil || t.message == nil || t.message.Chat == nil || t.message.ID <= 0 || t.message.Chat.ID == 0 {
		return "", fmt.Errorf("reaction requires a real incoming message with a nonzero chat and positive message ID")
	}
	var parsed struct {
		Emoji string `json:"emoji"`
	}
	dec := json.NewDecoder(strings.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("invalid arguments: expected one JSON object")
	}
	if !slices.Contains(replyReactionEmojis, parsed.Emoji) {
		return "", fmt.Errorf("unsupported emoji; choose one of %s", strings.Join(replyReactionEmojis, " "))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if t.bot == nil {
		return "", fmt.Errorf("bot is not initialized")
	}
	available, constrained, err := t.bot.AvailableReactionEmojis(t.message.Chat.ID)
	if err != nil {
		return "", fmt.Errorf("could not check allowed reactions: %w", err)
	}
	if constrained && !slices.Contains(available, parsed.Emoji) {
		return "", fmt.Errorf("emoji %s is not allowed in this chat; allowed supported emojis: %s", parsed.Emoji, strings.Join(allowedReplyReactionEmojis(available), " "))
	}
	if err := t.bot.React(t.message, parsed.Emoji); err != nil {
		return "", fmt.Errorf("reaction was not sent: %w", err)
	}
	t.sent = true
	return `{"sent":true}`, nil
}

func allowedReplyReactionEmojis(available []string) []string {
	var allowed []string
	for _, emoji := range replyReactionEmojis {
		if slices.Contains(available, emoji) {
			allowed = append(allowed, emoji)
		}
	}
	return allowed
}

func (t *ReactToMessageTool) WasSent() bool { return t != nil && t.sent }
