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

	database.DB = mockDB
	EnsureTables()
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
		INSERT INTO users (id, username, first_name, last_name) VALUES
		(42, 'alice', 'Alice', 'Liddell'),
		(43, 'bob', 'Bob', 'Builder')
	`)
	if err != nil {
		t.Fatalf("Failed to insert users: %v", err)
	}

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

func TestPromptMgrPlugin_ProcessPersonFactCommands(t *testing.T) {
	originalConfigs := registry.Config
	defer func() {
		registry.Config = originalConfigs
	}()

	registry.Config.OwnerUsername = "test_owner"

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB
	EnsureTables()

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
		INSERT INTO users (id, username, first_name, last_name) VALUES
		(42, 'alice', 'Alice', 'Liddell'),
		(43, 'bob', 'Bob', 'Builder')
	`)
	if err != nil {
		t.Fatalf("Failed to insert users: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)
	plugin := &PromptMgrPlugin{}

	t.Run("show current chat facts by username", func(t *testing.T) {
		if err := SavePersonFacts(123, 42, "likes tea"); err != nil {
			t.Fatalf("SavePersonFacts failed: %v", err)
		}
		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfact 123 @alice",
			Sender: &telebot.User{Username: "test_owner"},
			Chat:   &telebot.Chat{ID: 123, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		messageText, _ := mockBot.SendWhat.(string)
		if !strings.Contains(messageText, "likes tea") {
			t.Fatalf("expected facts in response, got %q", messageText)
		}
	})

	t.Run("update current chat facts by user id", func(t *testing.T) {
		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfact 123 43 likes hammers",
			Sender: &telebot.User{Username: "test_owner"},
			Chat:   &telebot.Chat{ID: 123, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		facts, err := GetPersonFacts(123, 43)
		if err != nil {
			t.Fatalf("GetPersonFacts failed: %v", err)
		}
		if facts != "likes hammers" {
			t.Fatalf("expected updated facts, got %q", facts)
		}
	})

	t.Run("update another chat facts from owner private chat", func(t *testing.T) {
		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfact -555 @alice likes chess",
			Sender: &telebot.User{Username: "test_owner"},
			Chat:   &telebot.Chat{ID: 999, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		facts, err := GetPersonFacts(-555, 42)
		if err != nil {
			t.Fatalf("GetPersonFacts failed: %v", err)
		}
		if facts != "likes chess" {
			t.Fatalf("expected other-chat facts, got %q", facts)
		}
	})

	t.Run("non owner is rejected", func(t *testing.T) {
		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfact 123 @alice",
			Sender: &telebot.User{Username: "not_owner"},
			Chat:   &telebot.Chat{ID: 123, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		messageText, _ := mockBot.SendWhat.(string)
		if !strings.Contains(messageText, "only the bot owner") {
			t.Fatalf("expected owner error, got %q", messageText)
		}
	})

	t.Run("missing chat id is rejected", func(t *testing.T) {
		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfact @alice",
			Sender: &telebot.User{Username: "test_owner"},
			Chat:   &telebot.Chat{ID: 123, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		messageText, _ := mockBot.SendWhat.(string)
		if !strings.Contains(messageText, "Invalid command format") {
			t.Fatalf("expected invalid format error, got %q", messageText)
		}
	})

	t.Run("list all person facts for chat", func(t *testing.T) {
		if err := SavePersonFacts(123, 42, "likes tea"); err != nil {
			t.Fatalf("SavePersonFacts failed: %v", err)
		}
		if err := SavePersonFacts(123, 43, "likes hammers"); err != nil {
			t.Fatalf("SavePersonFacts failed: %v", err)
		}

		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfacts 123",
			Sender: &telebot.User{Username: "test_owner"},
			Chat:   &telebot.Chat{ID: 123, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		messageText, _ := mockBot.SendWhat.(string)
		if !strings.Contains(messageText, "alice:") || !strings.Contains(messageText, "likes tea") {
			t.Fatalf("expected alice facts in output, got %q", messageText)
		}
		if !strings.Contains(messageText, "bob:") || !strings.Contains(messageText, "likes hammers") {
			t.Fatalf("expected bob facts in output, got %q", messageText)
		}
	})

	t.Run("personfacts non owner is rejected", func(t *testing.T) {
		mockBot.SendCalled = false
		plugin.Process(&telebot.Message{
			Text:   "!personfacts 123",
			Sender: &telebot.User{Username: "not_owner"},
			Chat:   &telebot.Chat{ID: 123, Type: telebot.ChatPrivate},
		})
		if !mockBot.SendCalled {
			t.Fatal("expected Send to be called")
		}
		messageText, _ := mockBot.SendWhat.(string)
		if !strings.Contains(messageText, "only the bot owner") {
			t.Fatalf("expected owner error, got %q", messageText)
		}
	})
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

	database.DB = mockDB
	EnsureTables()

	longPrompt := strings.Repeat("x", telegramMessageChunkSize+250)
	_, err := mockDB.Exec(
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

func TestPersonFactsHelpers(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	database.DB = mockDB
	EnsureTables()

	if err := SavePersonFacts(123, 1, "likes Go"); err != nil {
		t.Fatalf("SavePersonFacts failed: %v", err)
	}
	if err := SavePersonFacts(123, 1, "likes Go and tea"); err != nil {
		t.Fatalf("second SavePersonFacts failed: %v", err)
	}
	if err := SavePersonFacts(123, 2, "prefers Rust"); err != nil {
		t.Fatalf("SavePersonFacts for second user failed: %v", err)
	}
	if err := SavePersonFacts(999, 1, "other chat facts"); err != nil {
		t.Fatalf("SavePersonFacts for other chat failed: %v", err)
	}

	facts, err := GetPersonFacts(123, 1)
	if err != nil {
		t.Fatalf("GetPersonFacts failed: %v", err)
	}
	if facts != "likes Go and tea" {
		t.Fatalf("expected latest facts, got %q", facts)
	}

	multi, err := GetPersonFactsMulti(123, []int64{1, 2, 1, 3})
	if err != nil {
		t.Fatalf("GetPersonFactsMulti failed: %v", err)
	}
	if multi[1] != "likes Go and tea" {
		t.Fatalf("expected user 1 latest facts, got %q", multi[1])
	}
	if multi[2] != "prefers Rust" {
		t.Fatalf("expected user 2 facts, got %q", multi[2])
	}
	if _, ok := multi[3]; ok {
		t.Fatal("did not expect facts for missing user")
	}

	all, err := GetAllPersonFacts(123)
	if err != nil {
		t.Fatalf("GetAllPersonFacts failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 users in chat facts, got %d", len(all))
	}
	if all[1] != "likes Go and tea" || all[2] != "prefers Rust" {
		t.Fatalf("unexpected facts map: %#v", all)
	}
}
