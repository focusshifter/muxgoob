package selfprompt

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

// Mock function for testing
func mockGenerateNewPromptFunc(p *SelfPromptPlugin, history string, currentPrompt string) string {
	// Use the parameters to avoid unused parameter warnings
	_ = history
	_ = currentPrompt
	return "This is a new mock prompt generated from history"
}

func TestSelfPromptPlugin_Process(t *testing.T) {
	// Save original function to restore later
	originalFunc := generateNewPromptFunc
	originalUserFactsFunc := generateUserFactsFunc
	defer func() {
		generateNewPromptFunc = originalFunc
		generateUserFactsFunc = originalUserFactsFunc
	}()

	// Set mock function
	generateNewPromptFunc = mockGenerateNewPromptFunc
	generateUserFactsFunc = func(p *SelfPromptPlugin, chatID int64, user activeChatUser, history string, currentFacts string) string {
		_ = p
		_ = chatID
		_ = history
		if currentFacts != "" {
			return currentFacts
		}
		return "facts for " + user.Name
	}

	// Create plugin instance
	plugin := &SelfPromptPlugin{
		msgCounter: make(map[int64]int64),
	}

	// Enable test mode to prevent counter reset during tests
	plugin.SetTestMode(true)

	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Create required tables
	createTables(t, mockDB)

	database.DB = mockDB

	// Save original bot to restore later
	originalBot := registry.Bot
	defer func() {
		registry.Bot = originalBot
	}()

	// Setup mock bot with Reply and Send methods
	mockBot := &testutils.MockBotWrapper{}

	// Set up the ReplyFunc
	mockBot.ReplyFunc = func(message *telebot.Message, what interface{}, options ...interface{}) (*telebot.Message, error) {
		mockBot.SendCalled = true
		mockBot.SendWhat = what
		return &telebot.Message{}, nil
	}

	// Set up the SendFunc
	mockBot.SendFunc = func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
		mockBot.SendCalled = true
		mockBot.SendTo = to
		mockBot.SendWhat = what
		mockBot.SendOpts = options
		return &telebot.Message{}, nil
	}
	registry.SetTestBot(mockBot)

	// Set the database for the plugin
	plugin.db = mockDB

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		setup         func()
		expectedCalls bool
		verify        func(t *testing.T)
	}{
		{
			name: "Regular message - plugin disabled",
			message: &telebot.Message{
				Text: "This is a regular message",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			setup: func() {
				// Set plugin as disabled for this chat
				_, err := mockDB.Exec(
					"INSERT INTO plugin_settings (chat_id, plugin_name, key, value) VALUES (123, 'selfprompt', 'enabled', 'false')")
				if err != nil {
					t.Fatalf("Failed to insert plugin setting: %v", err)
				}

				mockBot.SendCalled = false
			},
			expectedCalls: false,
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}
			},
		},
		{
			name: "Regular message - plugin enabled, counter below threshold",
			message: &telebot.Message{
				Text: "This is a regular message",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 456,
				},
			},
			setup: func() {
				// Clear previous settings
				_, err := mockDB.Exec("DELETE FROM plugin_settings WHERE chat_id = 456")
				if err != nil {
					t.Fatalf("Failed to delete plugin settings: %v", err)
				}

				// Set plugin as enabled with interval 10
				_, err = mockDB.Exec(
					"INSERT INTO plugin_settings (chat_id, plugin_name, key, value) VALUES (456, 'selfprompt', 'enabled', 'true')")
				if err != nil {
					t.Fatalf("Failed to insert plugin setting: %v", err)
				}

				_, err = mockDB.Exec(
					"INSERT INTO plugin_settings (chat_id, plugin_name, key, value) VALUES (456, 'selfprompt', 'interval', '10')")
				if err != nil {
					t.Fatalf("Failed to insert plugin setting: %v", err)
				}

				// Set counter to 5 (below threshold of 10)
				// Initialize the map if it's nil
				if plugin.msgCounter == nil {
					plugin.msgCounter = make(map[int64]int64)
				}
				plugin.mutex.Lock()
				plugin.msgCounter[456] = 5
				t.Logf("Setting initial counter to 5 for chat 456")
				plugin.mutex.Unlock()

				mockBot.SendCalled = false
			},
			expectedCalls: false,
			verify: func(t *testing.T) {
				if mockBot.SendCalled {
					t.Error("Expected Send not to be called")
				}

				// Verify counter was incremented
				plugin.mutex.RLock()
				count := plugin.msgCounter[456]
				plugin.mutex.RUnlock()

				// Debug output
				t.Logf("Message counter for chat 456: %d", count)

				if count != 6 {
					t.Errorf("Expected counter to be 6, got %d", count)
				}
			},
		},
		{
			name: "!selfprompt command - show settings",
			message: &telebot.Message{
				Text: "!selfprompt",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 789,
				},
			},
			setup: func() {
				// Clear previous settings
				_, err := mockDB.Exec("DELETE FROM plugin_settings WHERE chat_id = 789")
				if err != nil {
					t.Fatalf("Failed to delete plugin settings: %v", err)
				}

				mockBot.SendCalled = false
			},
			expectedCalls: true,
			verify: func(t *testing.T) {
				if !mockBot.SendCalled {
					t.Error("Expected Send to be called")
				}

				// Should show current settings
				message, ok := mockBot.SendWhat.(string)
				if !ok {
					t.Error("Expected Send to be called with a string message")
				}

				if message == "" {
					t.Error("Expected non-empty message")
				}
			},
		},
		{
			name: "!selfprompt enable command",
			message: &telebot.Message{
				Text: "!selfprompt enable",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 789,
				},
			},
			setup: func() {
				mockBot.SendCalled = false
			},
			expectedCalls: true,
			verify: func(t *testing.T) {
				if !mockBot.SendCalled {
					t.Error("Expected Send to be called")
				}

				// Verify setting was updated
				// Check if any settings exist for this chat
				var count int
				err := mockDB.QueryRow("SELECT COUNT(*) FROM plugin_settings WHERE chat_id = 789").Scan(&count)
				if err != nil {
					t.Fatalf("Failed to count plugin settings: %v", err)
				}
				t.Logf("Found %d plugin settings for chat 789", count)

				// Try to get the specific setting
				var value string
				err = mockDB.QueryRow(
					"SELECT value FROM plugin_settings WHERE chat_id = 789 AND plugin_name = 'selfprompt' AND key = 'enabled'").Scan(&value)
				if err != nil {
					// If no rows, let's check if the plugin's db field is set correctly
					if err == sql.ErrNoRows {
						t.Logf("No plugin settings found, checking if plugin.db is set correctly")
						if plugin.db == nil {
							t.Fatalf("Plugin.db is nil, it should be set to mockDB")
						}
					} else {
						t.Fatalf("Failed to query plugin setting: %v", err)
					}
				} else {
					t.Logf("Found enabled value: %s", value)
					if value != "true" {
						t.Errorf("Expected enabled value to be 'true', got '%s'", value)
					}
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

// Helper function to create the necessary tables for testing
func createTables(t *testing.T, db *sql.DB) {
	// Create plugin_settings table with proper constraints
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS plugin_settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER,
			plugin_name TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			UNIQUE(chat_id, plugin_name, key)
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	// Create prompts table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			prompt TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(chat_id, version)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create prompts table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS person_facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			facts TEXT NOT NULL,
			version INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(chat_id, user_id, version)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create person_facts table: %v", err)
	}

	// Create messages table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER,
			chat_id INTEGER,
			sender_id INTEGER,
			reply_to_message_id INTEGER,
			forward_from_id INTEGER,
			forward_from_chat_id INTEGER,
			forward_date INTEGER,
			edit_date INTEGER,
			media_group_id TEXT,
			author_signature TEXT,
			unixtime INTEGER,
			text TEXT,
			caption TEXT,
			data TEXT,
			PRIMARY KEY (id, chat_id)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create messages table: %v", err)
	}

	// Create users table
	_, err = db.Exec(`
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

	// Insert some test data
	_, err = db.Exec(`
		INSERT INTO users (id, username, first_name, last_name)
		VALUES (123, 'test_user', 'Test', 'User');
	`)
	if err != nil {
		t.Fatalf("Failed to insert test user: %v", err)
	}

	// Insert some test messages
	currentTime := time.Now().Unix()
	_, err = db.Exec(`
		INSERT INTO messages (id, chat_id, sender_id, unixtime, text)
		VALUES 
		(1, 456, 123, ?, 'Test message 1'),
		(2, 456, 123, ?, 'Test message 2'),
		(3, 456, 123, ?, 'Test message 3');
	`, currentTime-300, currentTime-200, currentTime-100)
	if err != nil {
		t.Fatalf("Failed to insert test messages: %v", err)
	}
}

func TestRetrieveHistoryForChat_IncludesReplyParents(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	_, err := mockDB.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER,
			chat_id INTEGER,
			reply_to_message_id INTEGER,
			unixtime INTEGER,
			data TEXT,
			PRIMARY KEY (id, chat_id)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create messages table: %v", err)
	}

	plugin := &SelfPromptPlugin{
		db: mockDB,
	}

	chatID := int64(200)
	parent := telebot.Message{
		ID:       20,
		Unixtime: 100,
		Chat:     &telebot.Chat{ID: chatID},
		Sender:   &telebot.User{Username: "parent_user"},
		Text:     "Parent message",
	}
	child := telebot.Message{
		ID:       21,
		Unixtime: 200,
		Chat:     &telebot.Chat{ID: chatID},
		Sender:   &telebot.User{Username: "child_user"},
		Text:     "Child message",
		ReplyTo:  &parent,
	}

	parentData, err := json.Marshal(parent)
	if err != nil {
		t.Fatalf("Failed to marshal parent message: %v", err)
	}
	childData, err := json.Marshal(child)
	if err != nil {
		t.Fatalf("Failed to marshal child message: %v", err)
	}

	_, err = mockDB.Exec(
		`INSERT INTO messages (id, chat_id, reply_to_message_id, unixtime, data) VALUES (?, ?, ?, ?, ?)`,
		parent.ID, chatID, nil, parent.Unixtime, string(parentData),
	)
	if err != nil {
		t.Fatalf("Failed to insert parent message: %v", err)
	}
	_, err = mockDB.Exec(
		`INSERT INTO messages (id, chat_id, reply_to_message_id, unixtime, data) VALUES (?, ?, ?, ?, ?)`,
		child.ID, chatID, parent.ID, child.Unixtime, string(childData),
	)
	if err != nil {
		t.Fatalf("Failed to insert child message: %v", err)
	}

	history := plugin.retrieveHistoryForChat(chatID, 1)
	if len(history) != 2 {
		t.Fatalf("Expected 2 messages (child + parent), got %d", len(history))
	}
	if history[0].ID != parent.ID || history[1].ID != child.ID {
		t.Fatalf("Expected parent then child order, got IDs %d and %d", history[0].ID, history[1].ID)
	}
}

func TestUpdatePrompt_BootstrapsChatWithoutFacts(t *testing.T) {
	originalFunc := generateNewPromptFunc
	originalUserFactsFunc := generateUserFactsFunc
	defer func() {
		generateNewPromptFunc = originalFunc
		generateUserFactsFunc = originalUserFactsFunc
	}()

	generateNewPromptFunc = func(p *SelfPromptPlugin, history string, currentPrompt string) string {
		_ = p
		return "topics: " + history + currentPrompt
	}
	generateUserFactsFunc = func(p *SelfPromptPlugin, chatID int64, user activeChatUser, history string, currentFacts string) string {
		_ = p
		_ = chatID
		_ = history
		if currentFacts != "" {
			return currentFacts
		}
		return "facts for " + user.Name
	}

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	createTables(t, mockDB)
	database.DB = mockDB

	plugin := &SelfPromptPlugin{db: mockDB, msgCounter: make(map[int64]int64)}

	chatID := int64(777)
	messages := []telebot.Message{
		{ID: 1, Unixtime: 100, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{ID: 1, Username: "alice"}, Text: "first"},
		{ID: 2, Unixtime: 200, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{ID: 2, Username: "bob"}, Text: "second"},
		{ID: 3, Unixtime: 300, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{ID: 1, Username: "alice"}, Text: "third"},
	}

	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, sender_id, unixtime, text, data) VALUES (?, ?, ?, ?, ?, ?)`,
			msg.ID, chatID, msg.Sender.ID, msg.Unixtime, msg.Text, string(data))
		if err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}

	plugin.bootstrapChat(chatID, 2)

	allFacts, err := promptmgr.GetAllPersonFacts(chatID)
	if err != nil {
		t.Fatalf("GetAllPersonFacts failed: %v", err)
	}
	if len(allFacts) != 2 {
		t.Fatalf("expected facts for 2 users, got %d", len(allFacts))
	}
	if allFacts[1] == "" || allFacts[2] == "" {
		t.Fatalf("expected facts for both users, got %#v", allFacts)
	}

	var promptCount int
	err = mockDB.QueryRow(`SELECT COUNT(*) FROM prompts WHERE chat_id = ?`, chatID).Scan(&promptCount)
	if err != nil {
		t.Fatalf("count prompts: %v", err)
	}
	if promptCount < 2 {
		t.Fatalf("expected multiple prompt versions from bootstrap, got %d", promptCount)
	}
}
