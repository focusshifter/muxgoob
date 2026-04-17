package reply

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
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
	} else if message.Text == "gooby give me a mock response" {
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
	} else if message.Text == "gooby, сделай опрос?" {
		return actionOnlyReplyToken
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
	database.DB = mockDB
	promptmgr.EnsureTables()

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
			name: "Question with 'gooby' - action-only poll",
			message: &telebot.Message{
				Text: "gooby, сделай опрос?",
				Sender: &telebot.User{
					Username: "test_user",
				},
				Chat: &telebot.Chat{
					ID: 123,
				},
			},
			expectedCalls: false,
			expectedReply: "",
			rngValue:      0,
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
			name: "Command with 'gooby' and space",
			message: &telebot.Message{
				Text: "gooby give me a mock response",
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
				tc.name == "Command with 'gooby' and space" || // Direct command with space
				tc.name == "Command with 'gooby,' and line break" || // Command with line break
				tc.name == "Question with 'gooby' - Yes response" || tc.name == "Question with 'gooby' - No response" || tc.name == "Question with 'gooby' - action-only poll" // Questions

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

	chatID := int64(100)
	parent := telebot.Message{
		ID:       10,
		Unixtime: 100,
		Chat:     &telebot.Chat{ID: chatID},
		Sender:   &telebot.User{Username: "parent_user"},
		Text:     "Parent message",
	}
	child := telebot.Message{
		ID:       11,
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

	originalDB := sqliteDb
	sqliteDb = mockDB
	defer func() {
		sqliteDb = originalDB
	}()

	history := retrieveHistoryForChat(chatID, 1)
	if len(history) != 2 {
		t.Fatalf("Expected 2 messages (child + parent), got %d", len(history))
	}
	if history[0].ID != parent.ID || history[1].ID != child.ID {
		t.Fatalf("Expected parent then child order, got IDs %d and %d", history[0].ID, history[1].ID)
	}
}

func TestBuildSpotifyReviewContext_UsesStoredReviewText(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	_, err := mockDB.Exec(`
		CREATE TABLE IF NOT EXISTS spotify_reviews (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			item_key TEXT NOT NULL,
			review_url TEXT NOT NULL,
			review_text TEXT,
			created_at INTEGER,
			UNIQUE(type, item_key)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create spotify_reviews table: %v", err)
	}

	_, err = mockDB.Exec(
		"INSERT INTO spotify_reviews (type, item_key, review_url, review_text) VALUES (?, ?, ?, ?)",
		"album", "abc123", "https://telegra.ph/test", "Great album, surprisingly good.",
	)
	if err != nil {
		t.Fatalf("Failed to insert review: %v", err)
	}

	originalDB := sqliteDb
	sqliteDb = mockDB
	defer func() {
		sqliteDb = originalDB
	}()

	messages := []telebot.Message{
		{
			Text: "Check this https://open.spotify.com/album/abc123",
		},
	}
	context := buildSpotifyReviewContext(messages)
	if context == "" {
		t.Fatalf("Expected spotify review context, got empty")
	}
	if !strings.Contains(context, "Great album") {
		t.Fatalf("Expected review text in context, got: %s", context)
	}
}

func TestBuildSpotifyReviewContext_FetchesReviewWhenMissing(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	_, err := mockDB.Exec(`
		CREATE TABLE IF NOT EXISTS spotify_reviews (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			item_key TEXT NOT NULL,
			review_url TEXT NOT NULL,
			review_text TEXT,
			created_at INTEGER,
			UNIQUE(type, item_key)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create spotify_reviews table: %v", err)
	}

	_, err = mockDB.Exec(
		"INSERT INTO spotify_reviews (type, item_key, review_url, review_text) VALUES (?, ?, ?, ?)",
		"album", "xyz789", "https://telegra.ph/test", "",
	)
	if err != nil {
		t.Fatalf("Failed to insert review: %v", err)
	}

	originalDB := sqliteDb
	sqliteDb = mockDB
	defer func() {
		sqliteDb = originalDB
	}()

	originalFetch := fetchTelegraphReviewText
	fetchTelegraphReviewText = func(_ string) string {
		return "Recovered review text"
	}
	defer func() {
		fetchTelegraphReviewText = originalFetch
	}()

	messages := []telebot.Message{
		{
			Text: "https://open.spotify.com/album/xyz789",
		},
	}
	context := buildSpotifyReviewContext(messages)
	if !strings.Contains(context, "Recovered review text") {
		t.Fatalf("Expected recovered review text in context, got: %s", context)
	}

	var savedText string
	err = mockDB.QueryRow(
		"SELECT review_text FROM spotify_reviews WHERE type = 'album' AND item_key = ?",
		"xyz789",
	).Scan(&savedText)
	if err != nil {
		t.Fatalf("Failed to query saved review text: %v", err)
	}
	if savedText != "Recovered review text" {
		t.Fatalf("Expected saved review text, got: %s", savedText)
	}
}

func TestBuildPersonFactsContext_UsesHistoryUsersAndAsker(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

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

	originalSQLiteDB := sqliteDb
	originalDB := database.DB
	sqliteDb = mockDB
	database.DB = mockDB
	defer func() {
		sqliteDb = originalSQLiteDB
		database.DB = originalDB
	}()
	promptmgr.EnsureTables()

	if err := promptmgr.SavePersonFacts(123, 1, "likes Go"); err != nil {
		t.Fatalf("failed to save user 1 facts: %v", err)
	}
	if err := promptmgr.SavePersonFacts(123, 2, "likes Rust"); err != nil {
		t.Fatalf("failed to save user 2 facts: %v", err)
	}
	if err := promptmgr.SavePersonFacts(123, 99, "bot facts"); err != nil {
		t.Fatalf("failed to save bot facts: %v", err)
	}

	history := []telebot.Message{{Sender: &telebot.User{ID: 1, Username: "alice"}, Text: "hello"}}
	currentMessage := &telebot.Message{Sender: &telebot.User{ID: 2, Username: "bob"}, Text: "question"}

	context := buildPersonFactsContext(123, history, currentMessage, 99)
	if !strings.Contains(context, "alice: likes Go") {
		t.Fatalf("expected alice facts in context, got: %s", context)
	}
	if !strings.Contains(context, "bob: likes Rust") {
		t.Fatalf("expected bob facts in context, got: %s", context)
	}
	if strings.Contains(context, "bot facts") {
		t.Fatalf("did not expect bot facts in context, got: %s", context)
	}

	prefill := buildNoAssPrefill(history, "question", "system prompt", context, 99, currentMessage, []string{"alice", "bob"})
	if !strings.Contains(prefill, "Chat member profiles:") {
		t.Fatalf("expected profiles section in prefill, got: %s", prefill)
	}
	if !strings.Contains(prefill, "Chat members: alice, bob") {
		t.Fatalf("expected members section in prefill, got: %s", prefill)
	}
	if !strings.Contains(prefill, "alice: likes Go") || !strings.Contains(prefill, "bob: likes Rust") {
		t.Fatalf("expected person facts in prefill, got: %s", prefill)
	}
	if !strings.Contains(prefill, "{{user}} (bob): question") {
		t.Fatalf("expected current question in prefill, got: %s", prefill)
	}
}

func TestBuildPersonFactsContext_IncludesMentionedUserFromSameChatOnly(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

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

	_, err = mockDB.Exec(`INSERT INTO users (id, username, first_name, last_name, data) VALUES (1, 'alice', 'Alice', '', ''), (2, 'bob', 'Bob', '', ''), (3, 'pingeee', 'Ping', '', ''), (4, 'outsider', 'Out', '', '')`)
	if err != nil {
		t.Fatalf("Failed to seed users: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, sender_id, unixtime, text, data) VALUES (10, 123, 3, 1, 'hi', '{}'), (11, 999, 4, 1, 'yo', '{}')`)
	if err != nil {
		t.Fatalf("Failed to seed messages: %v", err)
	}

	originalSQLiteDB := sqliteDb
	originalDB := database.DB
	sqliteDb = mockDB
	database.DB = mockDB
	defer func() {
		sqliteDb = originalSQLiteDB
		database.DB = originalDB
	}()
	promptmgr.EnsureTables()

	if err := promptmgr.SavePersonFacts(123, 1, "likes Go"); err != nil {
		t.Fatalf("failed to save user 1 facts: %v", err)
	}
	if err := promptmgr.SavePersonFacts(123, 2, "likes Rust"); err != nil {
		t.Fatalf("failed to save user 2 facts: %v", err)
	}
	if err := promptmgr.SavePersonFacts(123, 3, "likes Doom"); err != nil {
		t.Fatalf("failed to save pingeee facts: %v", err)
	}
	if err := promptmgr.SavePersonFacts(999, 4, "likes secrets"); err != nil {
		t.Fatalf("failed to save outsider facts: %v", err)
	}

	history := []telebot.Message{{Sender: &telebot.User{ID: 1, Username: "alice"}, Text: "hello"}}
	currentMessage := &telebot.Message{Sender: &telebot.User{ID: 2, Username: "bob"}, Text: "Губи, что думаешь о @pingeee и @outsider?"}

	context := buildPersonFactsContext(123, history, currentMessage, 99)
	if !strings.Contains(context, "pingeee: likes Doom") {
		t.Fatalf("expected same-chat mentioned user facts in context, got: %s", context)
	}
	if strings.Contains(context, "outsider: likes secrets") {
		t.Fatalf("did not expect cross-chat mentioned user facts in context, got: %s", context)
	}
}
