package twitchstreams

import (
	"testing"
	"time"

	"github.com/nicklaw5/helix"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestTwitchstreamsPlugin_Process(t *testing.T) {
	// Save original configs to restore later
	originalConfigs := registry.Config
	defer func() {
		registry.Config = originalConfigs
	}()

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Create plugin instance
	plugin := &TwitchstreamsPlugin{}

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		expectedCalls bool
		expectedReply string
	}{
		{
			name: "!стрим command",
			message: &telebot.Message{
				Text: "!стрим",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "GIFF STREM OR RIOT (ノಠ益ಠ)ノ彡┻━┻",
		},
		{
			name: "!стрем command",
			message: &telebot.Message{
				Text: "!стрем",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "GIFF STREM OR RIOT (ノಠ益ಠ)ノ彡┻━┻",
		},
		{
			name: "!riot command",
			message: &telebot.Message{
				Text: "!riot",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: true,
			expectedReply: "GIFF STREM OR RIOT (ノಠ益ಠ)ノ彡┻━┻",
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
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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

func TestCheckStreams(t *testing.T) {
	// This is a simple test to ensure the function doesn't panic
	// A more comprehensive test would mock the Twitch API client

	// Save original configs to restore later
	originalConfigs := registry.Config
	originalClient := twitchClient
	defer func() {
		registry.Config = originalConfigs
		twitchClient = originalClient
	}()

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Set up minimal config
	registry.Config.TwitchStreams = []registry.TwitchStreamConfig{
		{
			ChatID:          123,
			TwitchUsernames: []string{"test_streamer"},
		},
	}

	// Create a mock twitch client to prevent nil pointer dereference
	twitchClient, _ = helix.NewClient(&helix.Options{
		ClientID:     "test_client_id",
		ClientSecret: "test_client_secret",
	})

	// Skip the actual API call by setting a future refresh time
	twitchTokenRefreshTime = time.Now().Add(24 * time.Hour)

	// Call the function - it should not panic now
	checkStreams(time.Now())
}
