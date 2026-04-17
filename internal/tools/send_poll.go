package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"github.com/focusshifter/muxgoob/registry"
)

type SendPollTool struct {
	chatID int64
	sent   bool
	send   func(chatID int64, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) error
}

type sendPollArgs struct {
	Question              string   `json:"question"`
	Options               []string `json:"options"`
	IsAnonymous           *bool    `json:"is_anonymous,omitempty"`
	AllowsMultipleAnswers *bool    `json:"allows_multiple_answers,omitempty"`
}

type sendPollResult struct {
	Sent                  bool     `json:"sent"`
	Question              string   `json:"question"`
	Options               []string `json:"options"`
	IsAnonymous           bool     `json:"is_anonymous"`
	AllowsMultipleAnswers bool     `json:"allows_multiple_answers"`
}

func NewSendPollTool(chatID int64) *SendPollTool {
	return &SendPollTool{chatID: chatID}
}

func (t *SendPollTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "sendPoll",
			Description: "Publish a Telegram poll directly in the current chat when the user asks to create, post, launch, or 'charge' an opрос/poll. Use this instead of replying with plain-text checkbox options. After using this tool successfully, do not send any follow-up confirmation message.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The poll question shown in Telegram.",
					},
					"options": map[string]any{
						"type":        "array",
						"description": "Poll answer options. Provide 2 to 10 short options.",
						"items": map[string]any{
							"type": "string",
						},
						"minItems": 2,
						"maxItems": 10,
					},
					"is_anonymous": map[string]any{
						"type":        "boolean",
						"description": "Whether the poll should be anonymous. Default true.",
					},
					"allows_multiple_answers": map[string]any{
						"type":        "boolean",
						"description": "Whether users can choose multiple options. Default false.",
					},
				},
				"required": []string{"question", "options"},
			},
		},
	}
}

func (t *SendPollTool) Execute(_ context.Context, args string) (string, error) {
	parsedArgs := sendPollArgs{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	question := strings.TrimSpace(parsedArgs.Question)
	if question == "" {
		return "", fmt.Errorf("question is required")
	}

	options := make([]string, 0, len(parsedArgs.Options))
	seen := map[string]struct{}{}
	for _, option := range parsedArgs.Options {
		trimmed := strings.TrimSpace(option)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		options = append(options, trimmed)
	}

	if len(options) < 2 {
		return "", fmt.Errorf("at least 2 unique options are required")
	}
	if len(options) > 10 {
		return "", fmt.Errorf("at most 10 options are allowed")
	}

	isAnonymous := true
	if parsedArgs.IsAnonymous != nil {
		isAnonymous = *parsedArgs.IsAnonymous
	}
	allowsMultipleAnswers := false
	if parsedArgs.AllowsMultipleAnswers != nil {
		allowsMultipleAnswers = *parsedArgs.AllowsMultipleAnswers
	}

	sender := t.send
	if sender == nil {
		sender = sendPollToChat
	}
	if err := sender(t.chatID, question, options, isAnonymous, allowsMultipleAnswers); err != nil {
		return "", err
	}

	t.sent = true
	return marshalJSON(sendPollResult{
		Sent:                  true,
		Question:              question,
		Options:               options,
		IsAnonymous:           isAnonymous,
		AllowsMultipleAnswers: allowsMultipleAnswers,
	}), nil
}

func (t *SendPollTool) WasSent() bool {
	return t != nil && t.sent
}

func sendPollToChat(chatID int64, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) error {
	if registry.Bot == nil {
		return fmt.Errorf("bot is not initialized")
	}

	_, err := registry.Bot.SendPoll(&pollRecipient{chatID: chatID}, question, options, isAnonymous, allowsMultipleAnswers)
	return err
}

type pollRecipient struct {
	chatID int64
}

func (r *pollRecipient) Recipient() string {
	return fmt.Sprintf("%d", r.chatID)
}
