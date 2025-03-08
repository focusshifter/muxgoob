package registry

import (
	"github.com/tucnak/telebot"
)

// BotInterface defines the interface for bot operations
// This is primarily used for testing purposes
type BotInterface interface {
	Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error)
}

// Ensure BotWrapper implements BotInterface
var _ BotInterface = (*BotWrapper)(nil)

// SetTestBot allows setting a test implementation of BotInterface for testing
func SetTestBot(testBot BotInterface) {
	// Create a wrapper that delegates to the test bot
	Bot = &BotWrapper{
		SendFunc: testBot.Send,
	}
}
