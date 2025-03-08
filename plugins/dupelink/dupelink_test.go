package dupelink

import (
	"strings"
	"testing"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestDupeLinkPlugin_Process(t *testing.T) {
	// Save original configs to restore later
	originalConfigs := registry.Config
	defer func() {
		registry.Config = originalConfigs
	}()

	// Setup test config
	registry.Config.DupeIgnoredDomains = []string{"ignored.com"}

	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Create required tables
	_, err := mockDB.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT,
			first_name TEXT,
			last_name TEXT,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create users table: %v", err)
	}

	_, err = mockDB.Exec(`
		CREATE TABLE IF NOT EXISTS dupe_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			sender_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			unixtime INTEGER NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create dupe_links table: %v", err)
	}

	// Insert test data
	_, err = mockDB.Exec(`
		INSERT INTO users (id, username, first_name, last_name)
		VALUES (123, 'test_user', 'Test', 'User');
	`)
	if err != nil {
		t.Fatalf("Failed to insert test user: %v", err)
	}

	_, err = mockDB.Exec(`
		INSERT INTO dupe_links (url, message_id, sender_id, chat_id, unixtime)
		VALUES ('example.com/page', 1, 123, 456, ?);
	`, time.Now().Unix()-3600) // 1 hour ago
	if err != nil {
		t.Fatalf("Failed to insert test dupe link: %v", err)
	}

	database.DB = mockDB

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Create plugin instance
	plugin := &DupeLinkPlugin{}

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		expectedCalls bool
		setup         func()
		verify        func(t *testing.T)
	}{
		{
			name: "Message with no URLs",
			message: &telebot.Message{
				Text:     "This is a message with no URLs",
				Entities: []telebot.MessageEntity{},
				Sender: &telebot.User{
					ID:        789,
					Username:  "another_user",
					FirstName: "Another",
					LastName:  "User",
				},
				Chat: &telebot.Chat{
					ID: 456,
				},
			},
			expectedCalls: false,
			setup: func() {
				mockBot.SendCalled = false
			},
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}
			},
		},
		{
			name: "Message with new URL",
			message: &telebot.Message{
				Text: "Check out https://newsite.com/pag",
				Entities: []telebot.MessageEntity{
					{
						Type:   "url",
						Offset: 10,
						Length: 23,
					},
				},
				Sender: &telebot.User{
					ID:        789,
					Username:  "another_user",
					FirstName: "Another",
					LastName:  "User",
				},
				Chat: &telebot.Chat{
					ID: 456,
				},
				Unixtime: time.Now().Unix(),
			},
			expectedCalls: false,
			setup: func() {
				mockBot.SendCalled = false
			},
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}

				// Verify the link was saved
				var count int
				// The URL is saved as hostname + requestURI, which is "newsite.com/pag"
				// Note: In the test, the URL is "newsite.com/pag" not "newsite.com/page"
				err := mockDB.QueryRow("SELECT COUNT(*) FROM dupe_links WHERE url = 'newsite.com/pag'").Scan(&count)
				if err != nil {
					t.Fatalf("Failed to query dupe_links: %v", err)
				}
				if count != 1 {
					// Let's check what URLs are actually in the database
					rows, err := mockDB.Query("SELECT url FROM dupe_links")
					if err != nil {
						t.Fatalf("Failed to query all dupe_links: %v", err)
					}
					defer rows.Close()

					var urls []string
					for rows.Next() {
						var url string
						if err := rows.Scan(&url); err != nil {
							t.Fatalf("Failed to scan URL: %v", err)
						}
						urls = append(urls, url)
					}

					t.Logf("Found URLs in database: %v", urls)
					t.Errorf("Expected 1 dupe_link record for 'newsite.com/pag', got %d", count)
				}
			},
		},
		{
			name: "Message with duplicate URL",
			message: &telebot.Message{
				Text: "Check out https://example.com/page",
				Entities: []telebot.MessageEntity{
					{
						Type:   "url",
						Offset: 10,
						Length: 24,
					},
				},
				Sender: &telebot.User{
					ID:        789,
					Username:  "another_user",
					FirstName: "Another",
					LastName:  "User",
				},
				Chat: &telebot.Chat{
					ID: 456,
				},
			},
			expectedCalls: true,
			setup: func() {
				mockBot.SendCalled = false
			},
			verify: func(t *testing.T) {
				if !mockBot.SendCalled {
					t.Error("Expected Send to be called")
				}

				// Verify the message contains the expected text
				message, ok := mockBot.SendWhat.(string)
				if !ok {
					t.Error("Expected Send to be called with a string message")
				}
				if message == "" || !contains(message, "already posted") || !contains(message, "Test User") {
					t.Errorf("Unexpected message: %s", message)
				}
			},
		},
		{
			name: "Message with ignored domain",
			message: &telebot.Message{
				Text: "Check out https://ignored.com/page",
				Entities: []telebot.MessageEntity{
					{
						Type:   "url",
						Offset: 10,
						Length: 23,
					},
				},
				Sender: &telebot.User{
					ID:        789,
					Username:  "another_user",
					FirstName: "Another",
					LastName:  "User",
				},
				Chat: &telebot.Chat{
					ID: 456,
				},
			},
			expectedCalls: false,
			setup: func() {
				mockBot.SendCalled = false
			},
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}

				// Verify the link was not saved
				var count int
				err := mockDB.QueryRow("SELECT COUNT(*) FROM dupe_links WHERE url = 'ignored.com/page'").Scan(&count)
				if err != nil {
					t.Fatalf("Failed to query dupe_links: %v", err)
				}
				if count != 0 {
					t.Errorf("Expected 0 dupe_link records, got %d", count)
				}
			},
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			plugin.Process(tc.message)
			tc.verify(t)
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return s != "" && substr != "" && strings.Contains(s, substr)
}
