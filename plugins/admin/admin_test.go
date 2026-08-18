package admin

import (
	"os"
	"path/filepath"
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

func TestAiChatDiagnosticShowsEffectiveAndRawScopes(t *testing.T) {
	originalConfig := registry.Config
	defer func() { registry.Config = originalConfig }()
	registry.Config.AiProvider = "config-provider"
	registry.Config.AiModel = "config-model"

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	if _, err := mockDB.Exec(`CREATE TABLE prompts (chat_id INTEGER, version INTEGER, prompt TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := mockDB.Exec(`INSERT INTO prompts (chat_id, version, prompt) VALUES (0, 2, 'global prompt'), (-100123, 3, 'chat prompt')`); err != nil {
		t.Fatal(err)
	}
	chatID := int64(-100123)
	if err := registry.SetAiModel(nil, "global-gpt-5-mini"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAiModel(&chatID, "chat-gpt-5.4-mini"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPluginSetting(nil, "other-plugin", "experimental_model", "global-other"); err != nil {
		t.Fatal(err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)
	plugin := &AdminPlugin{}
	plugin.handleAiCommands(&telebot.Message{Text: "!ai chat -100123", Chat: &telebot.Chat{ID: 1}})
	response, ok := mockBot.SendWhat.(string)
	if !mockBot.SendCalled || !ok {
		t.Fatalf("expected diagnostic response, got %#v", mockBot.SendWhat)
	}
	for _, want := range []string{
		"AI diagnostics for chat -100123",
		"AI model: chat-gpt-5.4-mini",
		"chat: chat-gpt-5.4-mini | global: global-gpt-5-mini | config: config-model",
		"global other-plugin.experimental_model=global-other",
		"Reply system prompt (effective 26 chars):",
		"DB chat: v3, 11 chars | DB global: v2, 13 chars",
	} {
		if !strings.Contains(response, want) {
			t.Errorf("diagnostic missing %q:\n%s", want, response)
		}
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

func TestAiModelTargetsUseUniformSetAndResetSyntax(t *testing.T) {
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)
	plugin := &AdminPlugin{}
	chatID := int64(456)

	for _, command := range []string{
		"!ai model reply reply-chat-model 456",
		"!ai model image image-chat-model 456",
		"!ai model vision vision-chat-model 456",
		"!ai model image-prompt image-prompt-chat-model 456",
		"!ai model selfprompt selfprompt-chat-model 456",
	} {
		plugin.handleAiCommands(&telebot.Message{Text: command, Chat: &telebot.Chat{ID: 1}})
	}
	if got := registry.GetAiModel(&chatID); got != "reply-chat-model" {
		t.Fatalf("reply model = %q", got)
	}
	if got := registry.GetImageAiModel(&chatID); got != "image-chat-model" {
		t.Fatalf("image model = %q", got)
	}
	if got := registry.GetImageVisionModel(&chatID); got != "vision-chat-model" {
		t.Fatalf("vision model = %q", got)
	}
	if got := registry.GetImagePromptModel(&chatID); got != "image-prompt-chat-model" {
		t.Fatalf("image prompt model = %q", got)
	}
	if got := selfpromptplugin.GetModel(&chatID); got != "selfprompt-chat-model" {
		t.Fatalf("selfprompt model = %q", got)
	}

	for _, command := range []string{
		"!ai model reply global 456",
		"!ai model image global 456",
		"!ai model vision global 456",
		"!ai model image-prompt global 456",
		"!ai model selfprompt global 456",
	} {
		plugin.handleAiCommands(&telebot.Message{Text: command, Chat: &telebot.Chat{ID: 1}})
	}
	for _, key := range []string{registry.AiModelKey, registry.ImageAiModelKey, registry.ImageVisionModelKey, registry.ImagePromptModelKey} {
		overrides, err := registry.GetPluginSettingOverrides(chatID, registry.ConfigPluginName, key)
		if err != nil || overrides.Chat != nil {
			t.Fatalf("expected config.%s override cleared, got %+v err=%v", key, overrides, err)
		}
	}
	overrides, err := registry.GetPluginSettingOverrides(chatID, selfpromptplugin.PluginName, selfpromptplugin.ModelKey)
	if err != nil || overrides.Chat != nil {
		t.Fatalf("expected selfprompt override cleared, got %+v err=%v", overrides, err)
	}
}

func TestAdminPlugin_OpenAICodexProviderCommand(t *testing.T) {
	originalConfig := registry.Config
	defer func() {
		registry.Config = originalConfig
	}()
	registry.Config.OwnerUsername = "test_owner"
	registry.Config.AiProvider = "openrouter"

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &AdminPlugin{}
	plugin.Process(&telebot.Message{
		Text:   "!ai provider openai-codex",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})

	if !mockBot.SendCalled {
		t.Fatalf("Expected response for openai-codex provider command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Global AI provider set to: openai-codex") {
		t.Fatalf("Unexpected response: %v", mockBot.SendWhat)
	}
	if got := registry.GetAiProvider(nil); got != "openai-codex" {
		t.Fatalf("Expected provider openai-codex, got %q", got)
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

func TestAdminPlugin_ImageGenerationAllowlistCommand(t *testing.T) {
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
	targetChatID := int64(777)
	if registry.GetImageGenerationEnabled(targetChatID) {
		t.Fatalf("image generation should be disabled by default")
	}

	plugin.Process(&telebot.Message{
		Text:   "!ai images enable 777",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})
	if !mockBot.SendCalled {
		t.Fatalf("Expected response for image generation enable command")
	}
	if got, ok := mockBot.SendWhat.(string); !ok || !strings.Contains(got, "Image generation enabled for chat 777") {
		t.Fatalf("Unexpected enable response: %v", mockBot.SendWhat)
	}
	if !registry.GetImageGenerationEnabled(targetChatID) {
		t.Fatalf("image generation should be enabled for chat 777")
	}

	mockBot.SendCalled = false
	mockBot.SendWhat = nil
	plugin.Process(&telebot.Message{
		Text:   "!ai get 777",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})
	response, ok := mockBot.SendWhat.(string)
	if !mockBot.SendCalled || !ok || !strings.Contains(response, "Image generation: enabled") {
		t.Fatalf("Expected !ai get to include enabled image generation, got: %v", mockBot.SendWhat)
	}

	mockBot.SendCalled = false
	mockBot.SendWhat = nil
	plugin.Process(&telebot.Message{
		Text:   "!ai images disable 777",
		Sender: &telebot.User{Username: "test_owner"},
		Chat:   &telebot.Chat{Type: telebot.ChatPrivate},
	})
	if registry.GetImageGenerationEnabled(targetChatID) {
		t.Fatalf("image generation should be disabled for chat 777")
	}
}

func TestAdminPlugin_GetIncludesOpenAICodexStatus(t *testing.T) {
	originalConfig := registry.Config
	originalCodexHome := os.Getenv("CODEX_HOME")
	defer func() {
		registry.Config = originalConfig
		_ = os.Setenv("CODEX_HOME", originalCodexHome)
	}()
	registry.Config.OwnerUsername = "test_owner"
	registry.Config.AiProvider = "openai-codex"
	registry.Config.AiModel = "gpt-5.4"
	registry.Config.ImageAiModel = "google/gemini-3.1-flash-lite-preview"

	codexHome := t.TempDir()
	if err := os.Setenv("CODEX_HOME", codexHome); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"test-token","refresh_token":"refresh-token"}}`), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()
	database.DB = mockDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &AdminPlugin{}
	plugin.Process(&telebot.Message{
		Text:   "!ai get",
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
	if !strings.Contains(response, "Provider: openai-codex") {
		t.Fatalf("Expected provider in !ai get response, got: %s", response)
	}
	if !strings.Contains(response, "Codex auth: available") {
		t.Fatalf("Expected Codex auth status in !ai get response, got: %s", response)
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
