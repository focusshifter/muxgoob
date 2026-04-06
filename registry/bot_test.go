package registry

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tucnak/telebot"
)

func TestBotWrapperSendSplitsLongStringMessages(t *testing.T) {
	longText := strings.Repeat("a", telegramMessageChunkSize+250)
	assertSplitSendPreservesText(t, longText)
}

func TestBotWrapperSendSplitsLongStringMessagesWithoutLosingWhitespace(t *testing.T) {
	longText := strings.Repeat("word ", 799) + "\n" + strings.Repeat("tail ", 30)
	if len([]rune(longText)) <= telegramMessageChunkSize {
		t.Fatalf("test message must exceed chunk limit, got %d", len([]rune(longText)))
	}

	assertSplitSendPreservesText(t, longText)
}

func TestBotWrapperSendDoesNotSplitNonStringMessages(t *testing.T) {
	var calls int
	bot := &BotWrapper{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			calls++
			if _, ok := what.(*telebot.Photo); !ok {
				t.Fatalf("expected photo payload, got %T", what)
			}
			return &telebot.Message{}, nil
		},
	}

	photo := &telebot.Photo{Caption: strings.Repeat("b", telegramMessageChunkSize+250)}
	if _, err := bot.Send(&telebot.Chat{ID: 1}, photo); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if calls != 1 {
		t.Fatalf("expected 1 call for non-string payload, got %d", calls)
	}
}

func TestBotWrapperSendReturnsErrorFromChunkSend(t *testing.T) {
	longText := strings.Repeat("x", telegramMessageChunkSize+50)
	var calls int
	bot := &BotWrapper{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			calls++
			if calls == 2 {
				return nil, fmt.Errorf("boom")
			}
			return &telebot.Message{}, nil
		},
	}

	_, err := bot.Send(&telebot.Chat{ID: 1}, longText)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls before failure, got %d", calls)
	}
}

func TestBotWrapperReplyUsesSendSplittingAndReplyTo(t *testing.T) {
	longText := strings.Repeat("z", telegramMessageChunkSize+25)
	original := &telebot.Message{
		ID:   99,
		Chat: &telebot.Chat{ID: 123},
	}

	var calls int
	silent := telebot.Silent
	bot := &BotWrapper{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			calls++
			if to.(*telebot.Chat).ID != original.Chat.ID {
				t.Fatalf("expected send to original chat, got %#v", to)
			}
			if len(options) != 2 {
				t.Fatalf("expected two forwarded options, got %d", len(options))
			}
			if options[0] != silent {
				t.Fatalf("expected non-SendOptions argument to be preserved")
			}
			sendOpts, ok := options[1].(*telebot.SendOptions)
			if !ok || sendOpts == nil {
				t.Fatalf("expected *telebot.SendOptions, got %T", options[1])
			}
			if sendOpts.ReplyTo != original {
				t.Fatalf("expected ReplyTo to be original message")
			}
			text, ok := what.(string)
			if !ok {
				t.Fatalf("expected string payload, got %T", what)
			}
			if len([]rune(text)) > telegramMessageChunkSize {
				t.Fatalf("reply chunk exceeds limit: %d", len([]rune(text)))
			}
			return &telebot.Message{}, nil
		},
	}

	if _, err := bot.Reply(original, longText, silent); err != nil {
		t.Fatalf("Reply returned error: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected 2 chunk sends, got %d", calls)
	}
}

func TestBotWrapperReplyRejectsNilMessageOrChat(t *testing.T) {
	bot := &BotWrapper{}

	if _, err := bot.Reply(nil, "hello"); err == nil {
		t.Fatal("expected error for nil message")
	}

	if _, err := bot.Reply(&telebot.Message{}, "hello"); err == nil {
		t.Fatal("expected error for nil chat")
	}
}

func assertSplitSendPreservesText(t *testing.T, longText string) {
	t.Helper()

	var sent []string
	bot := &BotWrapper{
		SendFunc: func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
			text, ok := what.(string)
			if !ok {
				t.Fatalf("expected string payload, got %T", what)
			}
			sent = append(sent, text)
			return &telebot.Message{}, nil
		},
	}

	if _, err := bot.Send(&telebot.Chat{ID: 1}, longText); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if len(sent) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(sent))
	}

	for i, chunk := range sent {
		if got := len([]rune(chunk)); got > telegramMessageChunkSize {
			t.Fatalf("chunk %d exceeds limit: %d", i, got)
		}
	}

	if strings.Join(sent, "") != longText {
		t.Fatalf("joined chunks did not match original message")
	}
}
