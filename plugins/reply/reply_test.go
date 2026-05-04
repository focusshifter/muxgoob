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

	text := strings.TrimSpace(message.Text)
	if text == "" {
		text = strings.TrimSpace(message.Caption)
	}

	// Handle specific test cases
	if text == "gooby, give me a mock response" {
		return "This is a mock ChatGPT response"
	} else if text == "gooby give me a mock response" {
		return "This is a mock ChatGPT response"
	} else if text == "gooby,\n\ngive me a mock response" {
		return "This is a mock ChatGPT response"
	} else if text == "gooby, are you sure?" {
		return "Да"
	} else if text == "gooby, is this true?" {
		return "Нет"
	} else if text == "This is a reply to the bot" {
		return "Mock reply to bot message"
	} else if text == "gooby, is this a test?" {
		return "Да" // Return "Да" for this test case
	} else if text == "gooby, сделай опрос?" {
		return actionOnlyReplyToken
	} else if text == "губи, смотри, мем" {
		return "This is a mock ChatGPT response"
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
			name: "Command with caption mention",
			message: &telebot.Message{
				Caption: "губи, смотри, мем",
				Photo: &telebot.Photo{
					File: telebot.File{FileID: "photo-file-id"},
				},
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
				tc.name == "Command with caption mention" || // Caption-based direct command
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

func TestResolveImageTargetPrefersReplyPhoto(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	photoMessage := telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "alice"}, Unixtime: 100}
	photoData, err := json.Marshal(photoMessage)
	if err != nil {
		t.Fatalf("marshal photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 10, chatID, 100, string(photoData))
	if err != nil {
		t.Fatalf("insert photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 10, chatID, "photo", "file-reply", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert photo item: %v", err)
	}

	question := &telebot.Message{
		ID:      11,
		Chat:    &telebot.Chat{ID: chatID},
		Sender:  &telebot.User{Username: "bob"},
		Text:    "губи, что на картинке?",
		ReplyTo: &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}},
	}

	got, err := resolveImageTarget(mockDB, question)
	if err != nil {
		t.Fatalf("resolveImageTarget returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected image target, got nil")
	}
	if got.FileID != "file-reply" || got.MessageID != 10 || got.Source != imageSourceReply {
		t.Fatalf("unexpected target: %+v", got)
	}
}

func TestResolveImageTargetUsesReplyToPhotoPayloadWhenPhotoIsNotPersisted(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	question := &telebot.Message{
		ID:     11,
		Chat:   &telebot.Chat{ID: chatID},
		Sender: &telebot.User{Username: "bob"},
		Text:   "губи, что на картинке?",
		ReplyTo: &telebot.Message{
			ID:    10,
			Chat:  &telebot.Chat{ID: chatID},
			Photo: &telebot.Photo{File: telebot.File{FileID: "reply-payload-photo"}, Width: 640, Height: 480},
		},
	}

	got, err := resolveImageTarget(mockDB, question)
	if err != nil {
		t.Fatalf("resolveImageTarget returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected image target, got nil")
	}
	if got.FileID != "reply-payload-photo" || got.MessageID != 10 || got.Source != imageSourceReply || got.Width != 640 || got.Height != 480 {
		t.Fatalf("unexpected target: %+v", got)
	}
}

func TestResolveImageTargetFallsBackToLatestRecentPhoto(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	otherChatID := int64(999)
	for _, item := range []struct {
		messageID int
		chatID    int64
		unixtime  int64
		fileID    string
		typ       string
	}{
		{messageID: 10, chatID: chatID, unixtime: 100, fileID: "old-photo", typ: "photo"},
		{messageID: 11, chatID: chatID, unixtime: 110, fileID: "not-photo", typ: "document"},
		{messageID: 12, chatID: chatID, unixtime: 120, fileID: "latest-photo", typ: "photo"},
		{messageID: 13, chatID: otherChatID, unixtime: 130, fileID: "other-chat-photo", typ: "photo"},
	} {
		msg := telebot.Message{ID: item.messageID, Chat: &telebot.Chat{ID: item.chatID}, Unixtime: item.unixtime}
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal message %d: %v", item.messageID, err)
		}
		_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, item.messageID, item.chatID, item.unixtime, string(data))
		if err != nil {
			t.Fatalf("insert message %d: %v", item.messageID, err)
		}
		_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, item.messageID, item.chatID, item.typ, item.fileID, 1024, 768, 12345)
		if err != nil {
			t.Fatalf("insert media item %d: %v", item.messageID, err)
		}
	}

	question := &telebot.Message{ID: 14, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "bob"}, Text: "губи, explain meme"}
	got, err := resolveImageTarget(mockDB, question)
	if err != nil {
		t.Fatalf("resolveImageTarget returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected image target, got nil")
	}
	if got.FileID != "latest-photo" || got.MessageID != 12 || got.Source != imageSourceLatest {
		t.Fatalf("unexpected target: %+v", got)
	}
}

func TestResolveImageTargetReturnsNilWhenNoPhotoFound(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	question := &telebot.Message{ID: 1, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "bob"}, Text: "губи, что там вообще?"}
	got, err := resolveImageTarget(mockDB, question)
	if err != nil {
		t.Fatalf("resolveImageTarget returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil target, got %+v", got)
	}
}

func TestShouldForceInspectRecentImage(t *testing.T) {
	testCases := []struct {
		name     string
		question string
		want     bool
	}{
		{name: "russian image question", question: "губи, что на картинке?", want: true},
		{name: "meme question", question: "gooby, explain meme", want: true},
		{name: "casual what there", question: "губи, что там вообще?", want: true},
		{name: "non-image retrospective", question: "обсуждали ли мы spotify раньше?", want: false},
		{name: "generic prompt", question: "gooby, придумай шутку", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldForceInspectRecentImage(tc.question)
			if got != tc.want {
				t.Fatalf("shouldForceInspectRecentImage(%q) = %v, want %v", tc.question, got, tc.want)
			}
		})
	}
}

func TestShouldUseImageInspection(t *testing.T) {
	replyTarget := &ResolvedImageTarget{ChatID: 123, MessageID: 10, FileID: "photo-reply", Source: imageSourceReply}
	latestTarget := &ResolvedImageTarget{ChatID: 123, MessageID: 11, FileID: "photo-latest", Source: imageSourceLatest}

	if !shouldUseImageInspection("губи, найс картинка?", &telebot.Message{ReplyTo: &telebot.Message{ID: 10}}, replyTarget) {
		t.Fatal("expected image inspection when question replies to a photo")
	}
	if !shouldUseImageInspection("губи, я именно про бадейку, чтоб поварешка тонула", &telebot.Message{ReplyTo: &telebot.Message{ID: 10}}, replyTarget) {
		t.Fatal("expected image inspection for reply-to-photo follow-up without explicit image keywords")
	}
	if !shouldUseImageInspection("губи, зарейтингуй хавку", &telebot.Message{ReplyTo: &telebot.Message{ID: 10}}, replyTarget) {
		t.Fatal("expected image inspection for reply-to-photo food rating request")
	}
	if !shouldUseImageInspection("губи, найс картинка?", &telebot.Message{Photo: &telebot.Photo{}}, latestTarget) {
		t.Fatal("expected image inspection when the current message itself contains a photo")
	}
	if shouldUseImageInspection("губи, что за еда на фото?", &telebot.Message{}, latestTarget) {
		t.Fatal("did not expect image inspection for a standalone text question that only matches the latest photo")
	}
	if !shouldUseImageInspection("губи, придумай шутку", &telebot.Message{ReplyTo: &telebot.Message{ID: 10}}, replyTarget) {
		t.Fatal("expected image inspection for any direct reply-to-photo prompt")
	}
	if shouldUseImageInspection("губи, найди сообщения про мем", &telebot.Message{ReplyTo: &telebot.Message{ID: 10}}, replyTarget) {
		t.Fatal("did not expect image inspection for history search question")
	}
	if shouldUseImageInspection("губи, что на картинке?", &telebot.Message{}, nil) {
		t.Fatal("did not expect image inspection without image target")
	}
}

func TestMaybeBuildImageInspectionContextUsesCaption(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	photoMessage := telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "alice"}, Unixtime: 100}
	photoData, err := json.Marshal(photoMessage)
	if err != nil {
		t.Fatalf("marshal photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 10, chatID, 100, string(photoData))
	if err != nil {
		t.Fatalf("insert photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 10, chatID, "photo", "file-reply", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert photo item: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	originalInspect := inspectRecentImageQuestion
	defer func() { inspectRecentImageQuestion = originalInspect }()
	inspectRecentImageQuestion = func(message *telebot.Message, target *ResolvedImageTarget) (string, error) {
		if strings.TrimSpace(message.Caption) != "губи, смотри, мем" {
			t.Fatalf("expected caption to reach image inspection, got text=%q caption=%q", message.Text, message.Caption)
		}
		return "на картинке вежливый кот и подпись с просьбой поздороваться", nil
	}

	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "bob"}, Caption: "губи, смотри, мем", Photo: &telebot.Photo{File: telebot.File{FileID: "photo-file-id"}}}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Caption)
	if !handled || fallback != "" {
		t.Fatalf("maybeBuildImageInspectionContext returned handled=%v fallback=%q context=%q", handled, fallback, context)
	}
	if !strings.Contains(context, imageInspectionContextIntro) || !strings.Contains(context, "вежливый кот") {
		t.Fatalf("expected structured image context, got %q", context)
	}
}

func TestMaybeBuildImageInspectionContextUsesReplyToPhotoFoodRatingPrompt(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	photoMessage := telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "alice"}, Unixtime: 100}
	photoData, err := json.Marshal(photoMessage)
	if err != nil {
		t.Fatalf("marshal photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 10, chatID, 100, string(photoData))
	if err != nil {
		t.Fatalf("insert photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 10, chatID, "photo", "file-reply", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert photo item: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	originalInspect := inspectRecentImageQuestion
	defer func() { inspectRecentImageQuestion = originalInspect }()
	inspectRecentImageQuestion = func(message *telebot.Message, target *ResolvedImageTarget) (string, error) {
		if target == nil || target.FileID != "file-reply" {
			t.Fatalf("unexpected target passed to inspectRecentImageQuestion: %+v", target)
		}
		return "это мем про тесты", nil
	}

	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "bob"}, Text: "губи, зарейтингуй хавку", ReplyTo: &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}}}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if !handled || fallback != "" {
		t.Fatalf("maybeBuildImageInspectionContext returned handled=%v fallback=%q context=%q", handled, fallback, context)
	}
	if !strings.Contains(context, "это мем про тесты") || !strings.Contains(context, message.Text) {
		t.Fatalf("expected image context to include summary and original food-rating prompt, got %q", context)
	}
}

func TestMaybeBuildImageInspectionContextReturnsFallbackWhenNoTarget(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	latestPhotoMessage := telebot.Message{ID: 20, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "alice"}, Unixtime: 200}
	latestPhotoData, err := json.Marshal(latestPhotoMessage)
	if err != nil {
		t.Fatalf("marshal latest photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 20, 123, 200, string(latestPhotoData))
	if err != nil {
		t.Fatalf("insert latest photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 20, 123, "photo", "latest-photo", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert latest photo item: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "bob"}, Text: "губи, что на картинке?"}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if !handled {
		t.Fatal("expected image question to be handled")
	}
	if context != "" || !strings.Contains(fallback, "Не вижу рядом картинки") {
		t.Fatalf("expected no-image fallback, got context=%q fallback=%q", context, fallback)
	}
}

func TestMaybeBuildImageInspectionContextSkipsFallbackWhenReplyHasTextContext(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	replyMessage := &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "alice"}, Text: "https://open.spotify.com/track/abc123"}
	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "bob"}, Text: "повторюсь, кстати. оч хорошо и мрачно", ReplyTo: replyMessage}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if handled || context != "" || fallback != "" {
		t.Fatalf("expected image context to be skipped silently when reply text provides alternate context, got handled=%v context=%q fallback=%q", handled, context, fallback)
	}
}

func TestMaybeBuildImageInspectionContextKeepsFallbackForExplicitImageQuestionOnTextReply(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	replyMessage := &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "alice"}, Text: "https://open.spotify.com/track/abc123"}
	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "bob"}, Text: "губи, что на картинке?", ReplyTo: replyMessage}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if !handled || context != "" || !strings.Contains(fallback, "Не вижу рядом картинки") {
		t.Fatalf("expected explicit no-image fallback for direct image question, got handled=%v context=%q fallback=%q", handled, context, fallback)
	}
}

func TestMaybeBuildImageInspectionContextKeepsFallbackForCaptionedPhotoReply(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	replyMessage := &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "alice"}, Caption: "мрачный спотифай превью", Photo: &telebot.Photo{}}
	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "bob"}, Text: "губи, что там вообще?", ReplyTo: replyMessage}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if !handled || context != "" || !strings.Contains(fallback, "Не вижу рядом картинки") {
		t.Fatalf("expected explicit no-image fallback for captioned photo reply, got handled=%v context=%q fallback=%q", handled, context, fallback)
	}
}

func TestMaybeBuildImageInspectionContextUsesReplyToPhotoGenericPrompt(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	chatID := int64(123)
	photoMessage := telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "alice"}, Unixtime: 100}
	photoData, err := json.Marshal(photoMessage)
	if err != nil {
		t.Fatalf("marshal photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 10, chatID, 100, string(photoData))
	if err != nil {
		t.Fatalf("insert photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 10, chatID, "photo", "file-reply", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert photo item: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	originalInspect := inspectRecentImageQuestion
	defer func() { inspectRecentImageQuestion = originalInspect }()
	called := false
	inspectRecentImageQuestion = func(message *telebot.Message, target *ResolvedImageTarget) (string, error) {
		called = true
		return "это еда на фото", nil
	}

	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: chatID}, Sender: &telebot.User{Username: "bob"}, Text: "губи, придумай шутку", ReplyTo: &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: chatID}}}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if !handled || fallback != "" {
		t.Fatalf("expected reply-to-photo prompt to build image context, got handled=%v context=%q fallback=%q", handled, context, fallback)
	}
	if !called {
		t.Fatal("expected inspectRecentImageQuestion to be called for reply-to-photo prompt")
	}
	if !strings.Contains(context, "это еда на фото") || !strings.Contains(context, message.Text) {
		t.Fatalf("expected image context to include summary and original prompt, got %q", context)
	}
}

func TestMaybeBuildImageInspectionContextIgnoresReplyToNonPhotoMessage(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	latestPhotoMessage := telebot.Message{ID: 20, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "alice"}, Unixtime: 200}
	latestPhotoData, err := json.Marshal(latestPhotoMessage)
	if err != nil {
		t.Fatalf("marshal latest photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 20, 123, 200, string(latestPhotoData))
	if err != nil {
		t.Fatalf("insert latest photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 20, 123, "photo", "latest-photo", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert latest photo item: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	originalInspect := inspectRecentImageQuestion
	defer func() { inspectRecentImageQuestion = originalInspect }()
	called := false
	inspectRecentImageQuestion = func(message *telebot.Message, target *ResolvedImageTarget) (string, error) {
		called = true
		return "не должно вызываться", nil
	}

	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "bob"}, Text: "*были бы", ReplyTo: &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 123}}}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if handled || context != "" || fallback != "" {
		t.Fatalf("expected non-photo reply to skip image inspection, got handled=%v context=%q fallback=%q", handled, context, fallback)
	}
	if called {
		t.Fatal("did not expect inspectRecentImageQuestion to run for non-photo reply")
	}
}

func TestMaybeBuildImageInspectionContextReturnsFallbackWhenReplyToPhotoMissing(t *testing.T) {
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
		CREATE TABLE IF NOT EXISTS media_items (
			message_id INTEGER,
			chat_id INTEGER,
			type TEXT,
			file_id TEXT,
			width INTEGER,
			height INTEGER,
			file_size INTEGER,
			data TEXT
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	latestPhotoMessage := telebot.Message{ID: 20, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "alice"}, Unixtime: 200}
	latestPhotoData, err := json.Marshal(latestPhotoMessage)
	if err != nil {
		t.Fatalf("marshal latest photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO messages (id, chat_id, unixtime, data) VALUES (?, ?, ?, ?)`, 20, 123, 200, string(latestPhotoData))
	if err != nil {
		t.Fatalf("insert latest photo message: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_items (message_id, chat_id, type, file_id, width, height, file_size, data) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`, 20, 123, "photo", "latest-photo", 1024, 768, 12345)
	if err != nil {
		t.Fatalf("insert latest photo item: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	message := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{Username: "bob"}, Text: "губи, я именно про бадейку, чтоб поварешка тонула", ReplyTo: &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 123}, Photo: &telebot.Photo{}}}
	context, fallback, handled := maybeBuildImageInspectionContext(message, message.Text)
	if !handled {
		t.Fatal("expected reply-to-photo follow-up to be handled even when replied photo is missing")
	}
	if context != "" || !strings.Contains(fallback, "Не вижу рядом картинки") {
		t.Fatalf("expected missing-photo fallback for reply-to-photo follow-up, got context=%q fallback=%q", context, fallback)
	}
}

func TestShouldForceSearchMessages(t *testing.T) {
	testCases := []struct {
		name     string
		question string
		want     bool
	}{
		{name: "retrospective discussion", question: "обсуждали ли мы spotify раньше?", want: true},
		{name: "who said something", question: "кто говорил про factorio?", want: true},
		{name: "find in history", question: "найди сообщения про мехворриор", want: true},
		{name: "present tense casual", question: "gooby, что думаешь про spotify?", want: false},
		{name: "simple direct prompt", question: "gooby, придумай шутку", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldForceSearchMessages(tc.question)
			if got != tc.want {
				t.Fatalf("shouldForceSearchMessages(%q) = %v, want %v", tc.question, got, tc.want)
			}
		})
	}
}

func TestFormatChatGPTRequestLogIncludesProvider(t *testing.T) {
	got := formatChatGPTRequestLog("openrouter", "openai/gpt-5.4:online", 686563, 26, 6)
	if !strings.Contains(got, "provider=openrouter") {
		t.Fatalf("expected provider in log line, got %q", got)
	}
	if !strings.Contains(got, "model=openai/gpt-5.4:online") {
		t.Fatalf("expected model in log line, got %q", got)
	}
	if !strings.Contains(got, "chat_id=686563") || !strings.Contains(got, "question_len=26") || !strings.Contains(got, "tools=6") {
		t.Fatalf("expected remaining fields in log line, got %q", got)
	}
}

func TestBuildNoAssPrefillIncludesImageMetadataForPhotoMessages(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	_, err := mockDB.Exec(`CREATE TABLE media_metadata (
		message_id INTEGER NOT NULL,
		chat_id INTEGER NOT NULL,
		media_type TEXT NOT NULL,
		file_id TEXT NOT NULL,
		file_unique_id TEXT,
		model TEXT NOT NULL,
		description TEXT NOT NULL,
		visible_text TEXT,
		tags TEXT,
		status TEXT NOT NULL DEFAULT 'done',
		error TEXT,
		created_at INTEGER DEFAULT 0,
		updated_at INTEGER DEFAULT 0,
		PRIMARY KEY (chat_id, message_id, file_id)
	)`)
	if err != nil {
		t.Fatalf("failed to create media_metadata: %v", err)
	}
	_, err = mockDB.Exec(`INSERT INTO media_metadata (message_id, chat_id, media_type, file_id, model, description, visible_text, tags, status) VALUES (10, 123, 'photo', 'file-cat', 'test-model', 'Шесть пукающих котов в мемном альбоме', '', 'кот,мем', 'done')`)
	if err != nil {
		t.Fatalf("failed to insert media metadata: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	history := []telebot.Message{{ID: 10, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{ID: 1, Username: "alice"}, Caption: "котики"}}
	currentMessage := &telebot.Message{ID: 11, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{ID: 2, Username: "bob"}, Text: "губи, что там было?"}

	prefill := buildNoAssPrefill(history, currentMessage.Text, "", "", 99, currentMessage, nil)
	if !strings.Contains(prefill, "{{user}} (alice): котики") {
		t.Fatalf("expected captioned photo message in prefill, got: %s", prefill)
	}
	if !strings.Contains(prefill, "Image metadata: Шесть пукающих котов") {
		t.Fatalf("expected image metadata in prefill, got: %s", prefill)
	}
}

func TestDescribeAndStoreImageMetadataPersistsVisionDescription(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	_, err := mockDB.Exec(`CREATE TABLE media_metadata (
		message_id INTEGER NOT NULL,
		chat_id INTEGER NOT NULL,
		media_type TEXT NOT NULL,
		file_id TEXT NOT NULL,
		file_unique_id TEXT,
		model TEXT NOT NULL,
		description TEXT NOT NULL,
		visible_text TEXT,
		tags TEXT,
		status TEXT NOT NULL DEFAULT 'done',
		error TEXT,
		created_at INTEGER DEFAULT 0,
		updated_at INTEGER DEFAULT 0,
		PRIMARY KEY (chat_id, message_id, file_id)
	)`)
	if err != nil {
		t.Fatalf("failed to create media_metadata: %v", err)
	}

	originalSQLiteDB := sqliteDb
	sqliteDb = mockDB
	defer func() { sqliteDb = originalSQLiteDB }()

	originalDescribe := describeImageForMetadata
	describeImageForMetadata = func(message *telebot.Message, target *ResolvedImageTarget) (ImageMetadataDescription, error) {
		return ImageMetadataDescription{Model: "test-model", Description: "рыжий кот выглядит виноватым после пука", Tags: []string{"кот", "мем"}}, nil
	}
	defer func() { describeImageForMetadata = originalDescribe }()

	message := &telebot.Message{ID: 10, Chat: &telebot.Chat{ID: 123}, Sender: &telebot.User{ID: 1, Username: "alice"}, Photo: &telebot.Photo{File: telebot.File{FileID: "file-cat"}, Width: 800, Height: 600}}
	if err := describeAndStoreImageMetadata(message); err != nil {
		t.Fatalf("describeAndStoreImageMetadata returned error: %v", err)
	}

	var description, tags, model string
	if err := mockDB.QueryRow(`SELECT description, tags, model FROM media_metadata WHERE chat_id = 123 AND message_id = 10 AND file_id = 'file-cat'`).Scan(&description, &tags, &model); err != nil {
		t.Fatalf("failed to read stored metadata: %v", err)
	}
	if description != "рыжий кот выглядит виноватым после пука" || model != "test-model" || !strings.Contains(tags, "кот") {
		t.Fatalf("unexpected metadata description=%q model=%q tags=%q", description, model, tags)
	}
}
