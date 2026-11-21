package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"
)

type reactionBotStub struct {
	available   []string
	constrained bool
	lookupErr   error
	sendErr     error
	calls       int
	target      *telebot.Message
	emoji       string
}

func (b *reactionBotStub) AvailableReactionEmojis(int64) ([]string, bool, error) {
	return b.available, b.constrained, b.lookupErr
}
func (b *reactionBotStub) React(m *telebot.Message, emoji string) error {
	b.calls++
	b.target = m
	b.emoji = emoji
	return b.sendErr
}

func TestReactToMessageValidationAndDelivery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    string
		message *telebot.Message
		bot     reactionBotStub
		wantErr string
	}{
		{name: "success", args: `{"emoji":"👍"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}, ReplyTo: &telebot.Message{ID: 7}}},
		{name: "allowed", args: `{"emoji":"😢"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, bot: reactionBotStub{available: []string{"😢"}, constrained: true}},
		{name: "synthetic", args: `{"emoji":"👍"}`, message: &telebot.Message{Chat: &telebot.Chat{ID: -100}}, wantErr: "real incoming message"},
		{name: "nil", args: `{"emoji":"👍"}`, wantErr: "real incoming message"},
		{name: "missing chat", args: `{"emoji":"👍"}`, message: &telebot.Message{ID: 42}, wantErr: "real incoming message"},
		{name: "unsupported", args: `{"emoji":"🚀"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, wantErr: "unsupported"},
		{name: "missing emoji", args: `{}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, wantErr: "unsupported"},
		{name: "invalid json", args: `oops`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, wantErr: "invalid arguments"},
		{name: "no target override", args: `{"emoji":"👍","message_id":7}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, wantErr: "invalid arguments"},
		{name: "disabled", args: `{"emoji":"👍"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, bot: reactionBotStub{constrained: true}, wantErr: "not allowed"},
		{name: "restricted", args: `{"emoji":"👍"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, bot: reactionBotStub{available: []string{"🔥"}, constrained: true}, wantErr: "not allowed"},
		{name: "lookup failure", args: `{"emoji":"👍"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, bot: reactionBotStub{lookupErr: fmt.Errorf("lookup unavailable")}, wantErr: "lookup unavailable"},
		{name: "send failure", args: `{"emoji":"👍"}`, message: &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}}, bot: reactionBotStub{sendErr: fmt.Errorf("Telegram rejected")}, wantErr: "Telegram rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := NewReactToMessageTool(tc.message)
			tool.bot = &tc.bot
			result, err := tool.Execute(context.Background(), tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || tool.WasSent() || strings.Contains(result, `"sent":true`) {
					t.Fatalf("result=%q err=%v sent=%v", result, err, tool.WasSent())
				}
				if tc.name != "send failure" && tc.bot.calls != 0 {
					t.Fatal("invalid reaction sent")
				}
				return
			}
			if err != nil || !tool.WasSent() || tc.bot.calls != 1 || tc.bot.target.ID != 42 || tc.bot.target.Chat.ID != -100 {
				t.Fatalf("result=%q err=%v bot=%+v", result, err, tc.bot)
			}
		})
	}
}

func TestReactionToolBindsCurrentMessage(t *testing.T) {
	message := &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}, ReplyTo: &telebot.Message{ID: 7}}
	tool := NewReactToMessageTool(message)
	bot := &reactionBotStub{}
	tool.bot = bot
	message.ID = 999
	message.Chat.ID = -999
	if _, err := tool.Execute(context.Background(), `{"emoji":"👍"}`); err != nil {
		t.Fatal(err)
	}
	if bot.target.ID != 42 || bot.target.Chat.ID != -100 {
		t.Fatalf("target changed: %+v", bot.target)
	}
}

func TestReactionToolIsOptionalForSubstantiveAnswers(t *testing.T) {
	tool := NewReactToMessageTool(&telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}})
	bot := &reactionBotStub{}
	tool.bot = bot
	reg := NewRegistry(tool)
	client := &mockCompletionClient{responses: []openai.ChatCompletionResponse{{Choices: []openai.ChatCompletionChoice{{FinishReason: openai.FinishReasonStop, Message: openai.ChatCompletionMessage{Content: "Here is the substantive answer"}}}}}}
	text, err := RunLoop(context.Background(), client, openai.ChatCompletionRequest{Tools: reg.Definitions()}, reg, 3)
	if err != nil || text != "Here is the substantive answer" || tool.WasSent() || bot.calls != 0 {
		t.Fatalf("text=%q err=%v sent=%v calls=%d", text, err, tool.WasSent(), bot.calls)
	}
}

func TestRunLoopReactionOnlyOrFallback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			bot := &reactionBotStub{}
			if fail {
				bot.sendErr = fmt.Errorf("reaction denied")
			}
			tool := NewReactToMessageTool(&telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}})
			tool.bot = bot
			client := &mockCompletionClient{responses: []openai.ChatCompletionResponse{
				{Choices: []openai.ChatCompletionChoice{{FinishReason: openai.FinishReasonToolCalls, Message: openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "Do not send this preamble", ToolCalls: []openai.ToolCall{
					{ID: "reaction", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "reactToMessage", Arguments: `{"emoji":"👍"}`}},
				}}}}},
				{Choices: []openai.ChatCompletionChoice{{FinishReason: openai.FinishReasonStop, Message: openai.ChatCompletionMessage{Content: "Substantive answer"}}}},
			}}
			tail := &actionStubTool{definition: openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "generateImage"}}}
			if !fail {
				client.responses[0].Choices[0].Message.ToolCalls = append(client.responses[0].Choices[0].Message.ToolCalls, openai.ToolCall{ID: "must_not_run", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "generateImage"}})
			}
			reg := NewRegistry(tool, tail)
			text, err := RunLoop(context.Background(), client, openai.ChatCompletionRequest{Tools: reg.Definitions()}, reg, 3)
			if tail.WasSent() {
				t.Fatal("executed another action after successful reaction")
			}
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if text != "Substantive answer" || client.callCount != 2 || tool.WasSent() {
					t.Fatalf("text=%q calls=%d", text, client.callCount)
				}
				messages := client.requests[1].Messages
				if !strings.Contains(messages[len(messages)-1].Content, "reaction denied") {
					t.Fatal("failure not returned to model")
				}
			} else if text != "" || client.callCount != 1 || !tool.WasSent() {
				t.Fatalf("text=%q calls=%d", text, client.callCount)
			}
		})
	}
}
