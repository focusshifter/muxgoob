package reply

import (
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

// DeterministicRandomGenerator implements RandomGenerator for testing with deterministic behavior
type DeterministicRandomGenerator struct {
	Message       *telebot.Message
	ExpectedReply string
	ShouldRespond bool
}

func NewDeterministicRandomGenerator(message *telebot.Message, expectedReply string, shouldRespond bool) *DeterministicRandomGenerator {
	return &DeterministicRandomGenerator{
		Message:       message,
		ExpectedReply: expectedReply,
		ShouldRespond: shouldRespond,
	}
}

func (m *DeterministicRandomGenerator) Intn(n int) int {
	// For dota messages
	if m.Message.Text == "Let's play some dota!" && m.ShouldRespond {
		return 0 // Trigger response
	} else if m.Message.Text == "Anyone want to play dota?" && !m.ShouldRespond {
		return 1 // Don't trigger response (any non-zero value for modulo 50)
	}

	// For товарищ майор messages
	if m.Message.Text == "товарищ майор, доложите обстановку" && m.ExpectedReply == "Так точно!" {
		return 0 // Return 0 for "Так точно!"
	} else if m.Message.Text == "товарищ майор здесь?" && m.ExpectedReply == "Я за него." {
		return 1 // Return 1 for "Я за него."
	}

	// For yes/no responses
	if m.ExpectedReply == "Да" {
		return 2 // Even number for "Да" (using 2 to avoid the modulo 100 case)
	} else if m.ExpectedReply == "Нет" {
		return 1 // Odd number for "Нет"
	}

	// Default behavior
	return 0
}

// MockChatGptClient implements ChatGptClient for testing
type MockChatGptClient struct{}

func (m *MockChatGptClient) Ask(message *telebot.Message) string {
	// Safety check for nil message
	if message == nil {
		return ""
	}

	// Handle specific test cases
	if message.Text == "gooby, give me a mock response" {
		return "This is a mock ChatGPT response"
	} else if message.Text == "gooby,\n\ngive me a mock response" {
		return "This is a mock ChatGPT response"
	} else if message.Text == "gooby, are you sure?" {
		return "Да"
	} else if message.Text == "gooby, is this true?" {
		return "Нет"
	} else if message.Text == "This is a reply to the bot" {
		return "Mock reply to bot message"
	} else if message.Text == "gooby, is this a test?" {
		return "Да" // Return "Да" for this test case
	}

	return ""
}

func TestReplyPlugin_Process(t *testing.T) {
	// Save original configs to restore later
	originalConfigs := registry.Config
	defer func() {
		registry.Config = originalConfigs
	}()

	// Setup test config
	registry.Config.ReplyTechLink = "https://example.com/tech"

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	// Set the bot's Me field to prevent nil pointer dereference
	mockBot.Me = &telebot.User{
		Username: "test_bot",
	}
	registry.SetTestBot(mockBot)

	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Set the sqliteDb variable to use our mock database
	sqliteDb = mockDB

	// Create plugin instance with mock dependencies
	plugin := &ReplyPlugin{}
	// We'll set specific random values for each test case

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		expectedCalls bool
		expectedReply string
		rngValue      int
	}{
		{
			name: "Tech command",
			message: &telebot.Message{
				Text: "!ттх",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "ТТХ: https://example.com/tech",
			rngValue:      0,
		},
		{
			name: "Question with 'gooby' - Yes response",
			message: &telebot.Message{
				Text: "gooby, is this a test?",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Да",
			rngValue:      2, // Even number for "Да"
		},
		{
			name: "Question with 'gooby' - No response",
			message: &telebot.Message{
				Text: "gooby, is this true?",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Нет",
			rngValue:      0, // In test mode, this will always return "Нет" for this specific question
		},
		{
			name: "Command with 'gooby,'",
			message: &telebot.Message{
				Text: "gooby, give me a mock response",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "This is a mock ChatGPT response",
			rngValue:      0,
		},
		{
			name: "Command with 'gooby,' and line break",
			message: &telebot.Message{
				Text: "gooby,\n\ngive me a mock response",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "This is a mock ChatGPT response",
			rngValue:      0,
		},
		{
			name: "Message with 'dota' - triggered",
			message: &telebot.Message{
				Text: "Let's play some dota!",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Щяб в дотку!",
			rngValue:      0, // In test mode, this specific message will always trigger the response
		},
		{
			name: "Message with 'dota' - not triggered",
			message: &telebot.Message{
				Text: "Anyone want to play dota?", // Different text from the triggered case
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: false,
			rngValue:      0, // In test mode, this message won't trigger the response
		},
		{
			name: "Message with 'товарищ майор' - 'Так точно!' response",
			message: &telebot.Message{
				Text: "товарищ майор, доложите обстановку",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Так точно!",
			rngValue:      0, // In test mode, this specific message will always trigger "Так точно!"
		},
		{
			name: "Message with 'товарищ майор' - 'Я за него.' response",
			message: &telebot.Message{
				Text: "товарищ майор здесь?",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Я за него.",
			rngValue:      0, // In test mode, this specific message will always trigger "Я за него."
		},
		{
			name: "Reply to bot's message",
			message: &telebot.Message{
				Text: "This is a reply to the bot",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
				// Make sure ReplyTo has all required fields
				ReplyTo: &telebot.Message{
					Sender: &telebot.User{
						Username: "test_bot", // This should match mockBot.Me.Username
					},
					// Add a Chat field to prevent nil pointer dereference
					Chat: &telebot.Chat{
						ID: 123,
					},
				},
			},
			expectedCalls: true,
			expectedReply: "Mock reply to bot message", // Our mock returns this for the reply case
			rngValue:      0,
		},
		{
			name: "Regular message - no response",
			message: &telebot.Message{
				Text: "This is a regular message",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: false,
			rngValue:      0,
		},
	}

	// No need to initialize rng anymore as we're using dependency injection

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock dependencies for this test case
			mockRandom := NewDeterministicRandomGenerator(tc.message, tc.expectedReply, tc.expectedCalls)
			mockChatClient := &MockChatGptClient{}

			// Set the dependencies for this test case
			plugin.SetDependencies(mockRandom, mockChatClient)

			// Reset mock bot state
			mockBot.SendCalled = false
			mockBot.SendWhat = nil
			mockBot.NotifyCalled = false
			mockBot.NotifyTo = nil
			mockBot.NotifyAction = ""

			// Process the message
			plugin.Process(tc.message)

			// Check typing notification - should be sent for ChatGPT responses only
			shouldType := tc.message.ReplyTo != nil && tc.message.ReplyTo.Sender != nil && tc.message.ReplyTo.Sender.Username == "test_bot" || // Reply to bot
				tc.name == "Command with 'gooby,'" || // Direct command
				tc.name == "Command with 'gooby,' and line break" || // Command with line break
				tc.name == "Question with 'gooby' - Yes response" || tc.name == "Question with 'gooby' - No response" // Questions

			// Verify typing notification
			if shouldType && !mockBot.NotifyCalled {
				t.Error("Expected typing notification, but none was sent")
			} else if !shouldType && mockBot.NotifyCalled {
				t.Error("Did not expect typing notification, but one was sent")
			}
			if shouldType && mockBot.NotifyAction != telebot.Typing {
				t.Errorf("Expected typing action, got %v", mockBot.NotifyAction)
			}

			// Verify message sending
			if tc.expectedCalls {
				if !mockBot.SendCalled {
					t.Error("Expected Send to be called, but it wasn't")
				}

				if tc.expectedReply != "" {
					reply, ok := mockBot.SendWhat.(string)
					if !ok {
						t.Error("Expected Send to be called with a string message")
					}

					if reply != tc.expectedReply {
						t.Errorf("Expected reply '%s', got '%s'", tc.expectedReply, reply)
					}
				}
			} else {
				if mockBot.SendCalled {
					t.Errorf("Expected Send not to be called, but it was called with: %v", mockBot.SendWhat)
				}
			}
		})
	}
}
