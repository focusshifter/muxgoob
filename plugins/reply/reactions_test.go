package reply

import (
	"strings"
	"testing"

	chattools "github.com/focusshifter/muxgoob/internal/tools"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

func TestAppendReactionReplyTool(t *testing.T) {
	for _, id := range []int{0, -1, 42} {
		message := &telebot.Message{ID: id, Chat: &telebot.Chat{ID: -100}}
		tools, parts, tool := appendReactionReplyTool(message, nil, nil)
		if id <= 0 {
			if tool != nil || len(tools) != 0 || len(parts) != 0 {
				t.Fatalf("synthetic message %d has reaction tool", id)
			}
			continue
		}
		defs := chattools.NewRegistry(tools...).Definitions()
		if tool == nil || len(defs) != 1 || defs[0].Function.Name != "reactToMessage" {
			t.Fatalf("missing tool: %#v", defs)
		}
		instruction := strings.Join(parts, " ")
		for _, required := range []string{"optional", "acknowledgement", "substantive", "failure", "instead of text"} {
			if !strings.Contains(instruction, required) {
				t.Fatalf("missing %q in %q", required, instruction)
			}
		}
	}
}

type reactionReplyClient struct {
	calls int
	text  string
}

func (c *reactionReplyClient) Ask(*telebot.Message) string { c.calls++; return c.text }

type reactionReplyRandom int

func (r reactionReplyRandom) Intn(int) int { return int(r) }

func TestReactionReplyPreservesTriggersAndSuppressesText(t *testing.T) {
	oldBot := registry.Bot
	t.Cleanup(func() { registry.Bot = oldBot })
	for _, tc := range []struct {
		name, text string
		reply      bool
		random     int
		wantCalls  int
	}{
		{name: "reply to bot", text: "Спасибо!", reply: true, wantCalls: 1},
		{name: "handle", text: "@test_bot спасибо", wantCalls: 1},
		{name: "command", text: "губи, спасибо", wantCalls: 1},
		{name: "question branch no yes-no fallback", text: "губи?", wantCalls: 1},
		{name: "ambient short", text: "Спасибо!", wantCalls: 0},
		{name: "ambient long skipped", text: strings.Repeat("обычное сообщение ", 20), random: 1, wantCalls: 0},
		{name: "ambient long existing random trigger", text: strings.Repeat("обычное сообщение ", 20), random: 0, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sends := 0
			registry.Bot = &registry.BotWrapper{Bot: &telebot.Bot{Me: &telebot.User{ID: 99, Username: "test_bot"}},
				SendFunc: func(telebot.Recipient, interface{}, ...interface{}) (*telebot.Message, error) {
					sends++
					return &telebot.Message{}, nil
				},
				NotifyFunc: func(telebot.Recipient, telebot.ChatAction) error { return nil },
			}
			client := &reactionReplyClient{text: actionOnlyReplyToken}
			p := &ReplyPlugin{random: reactionReplyRandom(tc.random), chatClient: client}
			m := &telebot.Message{ID: 42, Chat: &telebot.Chat{ID: -100}, Sender: &telebot.User{ID: 1}, Text: tc.text}
			if tc.reply {
				m.ReplyTo = &telebot.Message{ID: 7, Sender: registry.Bot.Me}
			}
			p.Process(m)
			if client.calls != tc.wantCalls || sends != 0 {
				t.Fatalf("calls=%d want=%d sends=%d", client.calls, tc.wantCalls, sends)
			}
		})
	}
}

type dispatchedReactionReplyClient struct {
	reactionReplyClient
	dispatched int
}

func (c *dispatchedReactionReplyClient) AskDispatched(*telebot.Message) string {
	c.dispatched++
	return actionOnlyReplyToken
}

func TestDispatchedReplyDoesNotUseIncomingReactionPathOrLeakMarker(t *testing.T) {
	oldBot, oldOwner := registry.Bot, registry.Config.OwnerUsername
	t.Cleanup(func() { registry.Bot = oldBot; registry.Config.OwnerUsername = oldOwner })
	registry.Config.OwnerUsername = "owner"
	sends := 0
	registry.Bot = &registry.BotWrapper{Bot: &telebot.Bot{Me: &telebot.User{Username: "test_bot"}},
		SendFunc: func(telebot.Recipient, interface{}, ...interface{}) (*telebot.Message, error) {
			sends++
			return &telebot.Message{}, nil
		},
	}
	client := &dispatchedReactionReplyClient{}
	p := &ReplyPlugin{random: reactionReplyRandom(1), chatClient: client}
	p.Process(&telebot.Message{ID: 42, Chat: &telebot.Chat{ID: 1, Type: telebot.ChatPrivate}, Sender: &telebot.User{Username: "owner"}, Text: "!reply -100 thanks"})
	if client.calls != 0 || client.dispatched != 1 || sends != 0 {
		t.Fatalf("ordinary calls=%d dispatched=%d sends=%d", client.calls, client.dispatched, sends)
	}
}

func TestImageAcknowledgementRejectsSyntheticMessage(t *testing.T) {
	oldBot := registry.Bot
	t.Cleanup(func() { registry.Bot = oldBot })
	calls := 0
	registry.Bot = &registry.BotWrapper{ReactFunc: func(*telebot.Message, string) error { calls++; return nil }}
	reactToImageRequest(&telebot.Message{Chat: &telebot.Chat{ID: -100}})
	if calls != 0 {
		t.Fatal("reacted to a synthetic cron message")
	}
}
