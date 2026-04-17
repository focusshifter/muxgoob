package registry

import (
	"github.com/focusshifter/muxgoob/utils/testutils"
	"github.com/tucnak/telebot"
)

// BotInterface defines the interface for bot operations
// This is primarily used for testing purposes
type BotInterface interface {
	Send(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error)
	Reply(message *telebot.Message, what interface{}, options ...interface{}) (*telebot.Message, error)
	Notify(to telebot.Recipient, action telebot.ChatAction) error
	SendPoll(to telebot.Recipient, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) (*telebot.Message, error)
}

// Ensure BotWrapper implements BotInterface
var _ BotInterface = (*BotWrapper)(nil)

// SetTestBot allows setting a test implementation of BotInterface for testing
func SetTestBot(testBot BotInterface) {
	// Create a wrapper that delegates to the test bot
	Bot = &BotWrapper{
		SendFunc:     testBot.Send,
		ReplyFunc:    testBot.Reply,
		NotifyFunc:   testBot.Notify,
		SendPollFunc: testBot.SendPoll,
	}

	// If the test bot is a MockBotWrapper, copy its Me field to the Bot
	if mockBot, ok := testBot.(*testutils.MockBotWrapper); ok && mockBot.Me != nil {
		// Create a telebot.Bot instance if it doesn't exist
		if Bot.Bot == nil {
			Bot.Bot = &telebot.Bot{}
		}
		// Set the Me field
		Bot.Bot.Me = mockBot.Me
	}
}
