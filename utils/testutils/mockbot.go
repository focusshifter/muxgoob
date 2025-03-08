package testutils

import (
	"github.com/tucnak/telebot"
)

// MockBotWrapper implements registry.BotInterface for testing
type MockBotWrapper struct {
	SendCalled bool
	SendTo     telebot.Recipient
	SendWhat   interface{}
	SendOpts   []interface{}
}

// Send implements the Send method for the mock bot
func (m *MockBotWrapper) Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
	m.SendCalled = true
	m.SendTo = to
	m.SendWhat = what
	m.SendOpts = options
	return &telebot.Message{}, nil
}
