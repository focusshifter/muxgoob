package promptmgr

import (
	"strings"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestPromptMgrPlugin_Process(t *testing.T) {
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

	// Create prompts table
	_, err := mockDB.Exec(`
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

	database.DB = mockDB

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Create plugin instance
	plugin := &PromptMgrPlugin{}

	// Test cases
	testCases := []struct {
		name          string
		message       *telebot.Message
		expectedCalls bool
		setup         func()
		verify        func(t *testing.T)
	}{
		{
			name: "!prompt current - no prompts",
			message: &telebot.Message{
				Text: "!prompt current",
				Sender: &telebot.User{
					Username: "test_owner",
				},
				Chat: &telebot.Chat{
					ID:   123,
					Type: telebot.ChatPrivate,
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
			},
		},
		{
			name: "!prompt current <new_prompt>",
			message: &telebot.Message{
				Text: "!prompt current test prompt",
				Sender: &telebot.User{
					Username: "test_owner",
				},
				Chat: &telebot.Chat{
					ID:   123,
					Type: telebot.ChatPrivate,
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

				// Verify prompt was saved
				var prompt string
				var version int
				err := mockDB.QueryRow(`
					SELECT prompt, version FROM prompts 
					WHERE chat_id = ? 
					ORDER BY version DESC LIMIT 1`, 123).Scan(&prompt, &version)
				if err != nil {
					t.Fatalf("Error retrieving prompt: %v", err)
				}

				if prompt != "test prompt" {
					t.Errorf("Expected prompt 'test prompt', got '%s'", prompt)
				}

				if version != 1 {
					t.Errorf("Expected version 1, got %d", version)
				}
			},
		},
		{
			name: "!prompt <chat_id>",
			message: &telebot.Message{
				Text: "!prompt 456",
				Sender: &telebot.User{
					Username: "test_owner",
				},
				Chat: &telebot.Chat{
					ID:   123,
					Type: telebot.ChatPrivate,
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
			},
		},
		{
			name: "!prompt <chat_id> <new_prompt>",
			message: &telebot.Message{
				Text: "!prompt 456 test prompt for another chat",
				Sender: &telebot.User{
					Username: "test_owner",
				},
				Chat: &telebot.Chat{
					ID:   123,
					Type: telebot.ChatPrivate,
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

				// Verify prompt was saved
				var prompt string
				var version int
				err := mockDB.QueryRow(`
					SELECT prompt, version FROM prompts 
					WHERE chat_id = ? 
					ORDER BY version DESC LIMIT 1`, 456).Scan(&prompt, &version)
				if err != nil {
					t.Fatalf("Error retrieving prompt: %v", err)
				}

				if prompt != "test prompt for another chat" {
					t.Errorf("Expected prompt 'test prompt for another chat', got '%s'", prompt)
				}

				if version != 1 {
					t.Errorf("Expected version 1, got %d", version)
				}
			},
		},
		{
			name: "!prompt - non-owner",
			message: &telebot.Message{
				Text: "!prompt current",
				Sender: &telebot.User{
					Username: "not_owner",
				},
				Chat: &telebot.Chat{
					ID:   123,
					Type: telebot.ChatPrivate,
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

				// Verify error message
				messageText, ok := mockBot.SendWhat.(string)
				if !ok {
					t.Error("Expected Send to be called with a string message")
				}

				if messageText != "Sorry, only the bot owner can control prompts." {
					t.Errorf("Expected error message, got: %s", messageText)
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

func TestSplitMessage(t *testing.T) {
	text := strings.Repeat("a", telegramMessageChunkSize+50)

	chunks := splitMessage(text, telegramMessageChunkSize)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if len([]rune(chunk)) > telegramMessageChunkSize {
			t.Fatalf("chunk %d exceeds limit: %d", i, len([]rune(chunk)))
		}
	}

	if strings.Join(chunks, "") != text {
		t.Fatal("expected chunks to reconstruct original text")
	}
}

func TestShowCurrentPromptSplitsLongPrompt(t *testing.T) {
	originalConfigs := registry.Config
	defer func() {
		registry.Config = originalConfigs
	}()

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	_, err := mockDB.Exec(`
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

	database.DB = mockDB

	longPrompt := strings.Repeat("x", telegramMessageChunkSize+250)
	_, err = mockDB.Exec(
		"INSERT INTO prompts (chat_id, version, prompt, created_at) VALUES (?, 1, ?, 0)",
		123,
		longPrompt,
	)
	if err != nil {
		t.Fatalf("Failed to insert prompt: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	var sentChunks []string
	mockBot.SendFunc = func(to telebot.Recipient, what interface{}, options ...interface{}) (*telebot.Message, error) {
		msg, ok := what.(string)
		if !ok {
			t.Fatalf("expected string message, got %T", what)
		}
		sentChunks = append(sentChunks, msg)
		return &telebot.Message{}, nil
	}
	registry.SetTestBot(mockBot)

	message := &telebot.Message{Chat: &telebot.Chat{ID: 123}}
	showCurrentPrompt(123, registry.Bot, message)

	if len(sentChunks) < 2 {
		t.Fatalf("expected multiple sent chunks, got %d", len(sentChunks))
	}

	for i, chunk := range sentChunks {
		if len([]rune(chunk)) > telegramMessageChunkSize {
			t.Fatalf("sent chunk %d exceeds limit: %d", i, len([]rune(chunk)))
		}
	}

	if !strings.Contains(strings.Join(sentChunks, ""), longPrompt) {
		t.Fatal("expected sent chunks to include the full prompt")
	}
}
