package nametrigger

import (
	"math/rand"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestNametriggerPlugin_Process(t *testing.T) {
	// Save original configs and RNG to restore later
	originalConfigs := registry.Config
	originalRng := rng
	defer func() {
		registry.Config = originalConfigs
		rng = originalRng
	}()

	// Setup test config with deterministic triggers
	registry.Config.NametriggerConfig.Triggers = []registry.Trigger{
		{
			Usernames: []string{"test_user"},
			Chance:    1, // Always trigger (when rng.Int() % 1 == 0)
			Reply:     "Test reply 1",
		},
		{
			Usernames: []string{"test_user", "another_user"},
			Chance:    2, // Trigger 50% of the time
			Reply:     "Test reply 2",
		},
	}

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Create plugin instance and initialize it
	plugin := &NametriggerPlugin{}
	plugin.Start(nil)

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		expectedCalls bool
		expectedReply string
		rngValue      int
	}{
		{
			name: "Trigger for test_user with chance 1",
			message: &telebot.Message{
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Test reply 1",
			rngValue:      0, // Any value will trigger when chance is 1
		},
		{
			name: "Trigger for test_user with chance 2 (even rng)",
			message: &telebot.Message{
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Test reply 1", // The first trigger always matches first
			rngValue:      2,              // Even number will trigger (2 % 2 == 0)
		},
		{
			name: "Trigger for test_user with chance 2 (odd rng)",
			message: &telebot.Message{
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Test reply 2", // The second trigger matches
			rngValue:      1,              // Odd number will trigger the second condition
		},
		{
			name: "Trigger for another_user with chance 2 (even rng)",
			message: &telebot.Message{
				Sender: &telebot.User{
					Username: "another_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "Test reply 2",
			rngValue:      4, // Even number will trigger (4 % 2 == 0)
		},
		{
			name: "No trigger for non-matching username",
			message: &telebot.Message{
				Sender: &telebot.User{
					Username: "random_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: false,
			rngValue:      0,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a deterministic RNG for testing
			rng = rand.New(rand.NewSource(int64(tc.rngValue)))

			// Reset mock bot state
			mockBot.SendCalled = false
			mockBot.SendWhat = nil

			// Process the message
			plugin.Process(tc.message)

			// Verify expectations
			if tc.expectedCalls {
				if !mockBot.SendCalled {
					t.Error("Expected Send to be called, but it wasn't")
				}

				reply, ok := mockBot.SendWhat.(string)
				if !ok {
					t.Error("Expected Send to be called with a string message")
				}

				if reply != tc.expectedReply {
					t.Errorf("Expected reply '%s', got '%s'", tc.expectedReply, reply)
				}
			} else {
				if mockBot.SendCalled {
					t.Errorf("Expected Send not to be called, but it was called with: %v", mockBot.SendWhat)
				}
			}
		})
	}
}
