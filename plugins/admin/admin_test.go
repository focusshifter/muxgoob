package admin

import (
	"strings"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	selfpromptplugin "github.com/focusshifter/muxgoob/plugins/selfprompt"
	"github.com/focusshifter/muxgoob/plugins/spotify"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestAdminPlugin_Process(t *testing.T) {
	// Save original configs to restore later
	originalConfigs := registry.Config
	defer func() {
		registry.Config = originalConfigs
	}()

	// Setup test config
	registry.Config.OwnerUsername = "test_owner"

	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Create chats table
	_, err := mockDB.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			id INTEGER PRIMARY KEY,
			type TEXT,
			title TEXT,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create chats table: %v", err)
	}

	// Insert test data
	_, err = mockDB.Exec(`
		INSERT INTO chats (id, type, title, username, first_name, last_name)
		VALUES 
		(123, 'group', 'Test Group', '', '', ''),
		(456, 'private', '', 'test_user', 'Test', 'User');
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	database.DB = mockDB

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Create plugin instance
	plugin := &AdminPlugin{}

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		expectedCalls bool
		verify        func(t *testing.T)
	}{
		{
			name: "!list command from owner",
			message: &telebot.Message{
				Text: "!list",
				Sender: &telebot.User{
					Username: "test_owner",
				},
				Chat: &telebot.Chat{
					Type: telebot.ChatPrivate,
				},
			},
			expectedCalls: true,
			verify: func(t *testing.T) {
				if !mockBot.SendCalled {
					t.Error("Expected Send to be called")
				}

				// Check that the response contains the chat information
				response, ok := mockBot.SendWhat.(string)
				if !ok {
					t.Error("Expected Send to be called with a string message")
				}

				if !contains(response, "Test Group") || !contains(response, "123") ||
					!contains(response, "test_user") || !contains(response, "456") {
					t.Errorf("Response does not contain expected chat information: %s", response)
				}
			},
		},
		{
			name: "!list command from non-owner",
			message: &telebot.Message{
				Text: "!list",
				Sender: &telebot.User{
					Username: "not_owner",
				},
				Chat: &telebot.Chat{
					Type: telebot.ChatPrivate,
				},
			},
			expectedCalls: false,
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}
			},
		},
		{
			name: "!list command in group chat",
			message: &telebot.Message{
				Text: "!list",
				Sender: &telebot.User{
					Username: "test_owner",
				},
				Chat: &telebot.Chat{
					Type: telebot.ChatGroup,
				},
			},
			expectedCalls: false,
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}
			},
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockBot.SendCalled = false
			plugin.Process(tc.message)
			tc.verify(t)
		})
	}
}

func TestAdminPlugin_SpotifyModelCommand(t *testing.T) {
	originalConfig := registry.Config
	defer func() {
		registry.Config = originalConfig
	}()
	registry.Config.OwnerUsername = "test_owner"

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &AdminPlugin{}

	globalModel := "openrouter/deepseek/deepseek-chat-v3.1"
	plugin.Process(&telebot.Message{
		Text: "!spotify model " + globalModel,
		Sender: &telebot.User{
			Username: "test_owner",
		},
		Chat: &telebot.Chat{
			Type: telebot.ChatPrivate,
		},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for global spotify model command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Global Spotify review model set to") {
		t.Fatalf("Unexpected global response: %v", mockBot.SendWhat)
	}
	if got := registry.GetPluginSetting(nil, spotify.SpotifyPluginName, spotify.SpotifyReviewModelKey, ""); got != globalModel {
		t.Fatalf("Expected global spotify model %q, got %q", globalModel, got)
	}

	mockBot.SendCalled = false
	mockBot.SendWhat = nil

	chatModel := "openrouter/meta-llama/llama-3.1-70b-instruct"
	targetChatID := int64(777)
	plugin.Process(&telebot.Message{
		Text: "!spotify model " + chatModel + " 777",
		Sender: &telebot.User{
			Username: "test_owner",
		},
		Chat: &telebot.Chat{
			Type: telebot.ChatPrivate,
		},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for chat spotify model command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Spotify review model for chat 777 set to") {
		t.Fatalf("Unexpected chat response: %v", mockBot.SendWhat)
	}
	if got := registry.GetPluginSetting(&targetChatID, spotify.SpotifyPluginName, spotify.SpotifyReviewModelKey, ""); got != chatModel {
		t.Fatalf("Expected chat spotify model %q, got %q", chatModel, got)
	}
}

func TestAdminPlugin_ImageModelCommand(t *testing.T) {
	originalConfig := registry.Config
	defer func() {
		registry.Config = originalConfig
	}()
	registry.Config.OwnerUsername = "test_owner"
	registry.Config.ImageAiModel = "google/gemini-3.1-flash-lite-preview"

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &AdminPlugin{}

	globalModel := "google/gemini-3.1-flash-lite-preview"
	plugin.Process(&telebot.Message{
		Text:   "!ai model image " + globalModel,
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for global image model command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Global image model set to") {
		t.Fatalf("Unexpected global response: %v", mockBot.SendWhat)
	}
	if got := registry.GetImageAiModel(nil); got != globalModel {
		t.Fatalf("Expected global image model %q, got %q", globalModel, got)
	}

	mockBot.SendCalled = false
	mockBot.SendWhat = nil

	targetChatID := int64(777)
	chatModel := "google/gemini-3.1-flash-lite-preview"
	plugin.Process(&telebot.Message{
		Text:   "!ai model image " + chatModel + " 777",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for chat image model command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Image model for chat 777 set to") {
		t.Fatalf("Unexpected chat response: %v", mockBot.SendWhat)
	}
	if got := registry.GetImageAiModel(&targetChatID); got != chatModel {
		t.Fatalf("Expected chat image model %q, got %q", chatModel, got)
	}

	mockBot.SendCalled = false
	mockBot.SendWhat = nil
	plugin.Process(&telebot.Message{
		Text:   "!ai get 777",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for !ai get")
	}
	response, ok := mockBot.SendWhat.(string)
	if !ok {
		t.Fatalf("Expected string response for !ai get, got %T", mockBot.SendWhat)
	}
	if !strings.Contains(response, "Image model: "+chatModel) {
		t.Fatalf("Expected !ai get response to include image model, got: %s", response)
	}
}

func TestAdminPlugin_SelfpromptCompressionModelCommand(t *testing.T) {
	originalConfig := registry.Config
	defer func() {
		registry.Config = originalConfig
	}()
	registry.Config.OwnerUsername = "test_owner"

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &AdminPlugin{}

	globalModel := "openrouter/google/gemini-2.5-flash"
	plugin.Process(&telebot.Message{
		Text:   "!ai model selfprompt " + globalModel,
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for global selfprompt model command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Global selfprompt model set to") {
		t.Fatalf("Unexpected global response: %v", mockBot.SendWhat)
	}
	if got := selfpromptplugin.GetModel(nil); got != globalModel {
		t.Fatalf("Expected global selfprompt model %q, got %q", globalModel, got)
	}

	mockBot.SendCalled = false
	mockBot.SendWhat = nil

	chatModel := "gpt-4o-mini"
	targetChatID := int64(777)
	plugin.Process(&telebot.Message{
		Text:   "!ai model selfprompt " + chatModel + " 777",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for chat selfprompt model command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Selfprompt model for chat 777 set to") {
		t.Fatalf("Unexpected chat response: %v", mockBot.SendWhat)
	}
	if got := selfpromptplugin.GetModel(&targetChatID); got != chatModel {
		t.Fatalf("Expected chat selfprompt model %q, got %q", chatModel, got)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return s != "" && substr != "" && strings.Contains(s, substr)
}
