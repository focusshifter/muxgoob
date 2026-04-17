package testutils

import (
	"github.com/tucnak/telebot"
)

// MockBotWrapper implements registry.BotInterface for testing
type MockBotWrapper struct {
	SendCalled        bool
	SendTo            telebot.Recipient
	SendWhat          interface{}
	SendOpts          []interface{}
	ReplyFunc         func(message *telebot.Message, what interface{}, options ...interface{}) (*telebot.Message, error)
	SendFunc          func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error)
	SendPollCalled    bool
	SendPollTo        telebot.Recipient
	SendPollQuestion  string
	SendPollOptions   []string
	SendPollAnonymous bool
	SendPollMultiple  bool
	SendPollFunc      func(to telebot.Recipient, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) (*telebot.Message, error)
	// Track notification calls
	NotifyCalled bool
	NotifyTo     telebot.Recipient
	NotifyAction telebot.ChatAction
	// Me represents the bot's own user information
	Me *telebot.User
}

// Send implements the Send method for the mock bot
func (m *MockBotWrapper) Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
	m.SendCalled = true
	m.SendTo = to
	m.SendWhat = what
	m.SendOpts = options

	if m.SendFunc != nil {
		return m.SendFunc(to, what, options...)
	}

	return &telebot.Message{}, nil
}

// Reply implements the Reply method for the mock bot
func (m *MockBotWrapper) Reply(message *telebot.Message, what interface{}, options ...interface{}) (*telebot.Message, error) {
	if m.ReplyFunc != nil {
		return m.ReplyFunc(message, what, options...)
	}
	// Default implementation if no ReplyFunc is provided
	return &telebot.Message{}, nil
}

// Notify implements the Notify method for the mock bot
func (m *MockBotWrapper) Notify(to telebot.Recipient, action telebot.ChatAction) error {
	m.NotifyCalled = true
	m.NotifyTo = to
	m.NotifyAction = action
	return nil
}

func (m *MockBotWrapper) SendPoll(to telebot.Recipient, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) (*telebot.Message, error) {
	m.SendPollCalled = true
	m.SendPollTo = to
	m.SendPollQuestion = question
	m.SendPollOptions = append([]string(nil), options...)
	m.SendPollAnonymous = isAnonymous
	m.SendPollMultiple = allowsMultipleAnswers

	if m.SendPollFunc != nil {
		return m.SendPollFunc(to, question, options, isAnonymous, allowsMultipleAnswers)
	}

	return &telebot.Message{}, nil
}
