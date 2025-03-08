package admin

import (
	"strings"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
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

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return s != "" && substr != "" && strings.Contains(s, substr)
}
