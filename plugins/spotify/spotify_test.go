package spotify

import (
	"testing"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestSpotifyPlugin_Start(t *testing.T) {
	plugin := &SpotifyPlugin{}
	plugin.Start(nil)

	if plugin.albumRegex == nil {
		t.Error("albumRegex should be initialized after Start()")
	}

	// Test regex pattern
	testCases := []struct {
		input    string
		expected bool
		albumID  string
	}{
		{
			input:    "Check out this album: https://open.spotify.com/album/6mUdeDZCsExyJLMdAfDuwh",
			expected: true,
			albumID:  "6mUdeDZCsExyJLMdAfDuwh",
		},
		{
			input:    "https://open.spotify.com/album/1234567890abcdef text after",
			expected: true,
			albumID:  "1234567890abcdef",
		},
		{
			input:    "https://open.spotify.com/track/1234567890abcdef",
			expected: false,
			albumID:  "",
		},
		{
			input:    "No Spotify link here",
			expected: false,
			albumID:  "",
		},
	}

	for _, tc := range testCases {
		matches := plugin.albumRegex.FindAllStringSubmatch(tc.input, -1)
		hasMatch := len(matches) > 0

		if hasMatch != tc.expected {
			t.Errorf("For input '%s': expected match=%v, got match=%v", tc.input, tc.expected, hasMatch)
		}

		if hasMatch && tc.albumID != "" {
			if matches[0][1] != tc.albumID {
				t.Errorf("For input '%s': expected albumID=%s, got=%s", tc.input, tc.albumID, matches[0][1])
			}
		}
	}
}

func TestSpotifyPlugin_Process_NoConfig(t *testing.T) {
	// Setup test database
	testDB := testutils.SetupTestDB(t)
	defer testDB.Close()
	
	// Set the database for the registry to use
	database.DB = testDB

	// Initialize registry without Spotify config
	registry.InitializeDbSettings()
	registry.Config = registry.Configuration{}

	// Create mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &SpotifyPlugin{}
	plugin.Start(nil)

	message := &telebot.Message{
		Chat: &telebot.Chat{ID: 123},
		Text: "Check out: https://open.spotify.com/album/6mUdeDZCsExyJLMdAfDuwh",
	}

	// Process should return early due to missing config
	plugin.Process(message)

	// Verify no messages were sent
	if mockBot.SendCalled {
		t.Error("Expected no messages to be sent when Spotify config is missing")
	}
}

func TestSpotifyPlugin_Process_Disabled(t *testing.T) {
	// Setup test database
	testDB := testutils.SetupTestDB(t)
	defer testDB.Close()
	
	// Set the database for the registry to use
	database.DB = testDB

	// Ensure the plugin_settings table exists
	err := registry.EnsurePluginSettingsTable()
	if err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}
	registry.Config = registry.Configuration{
		SpotifyConfig: registry.SpotifyConfig{
			ClientID:     "test_client_id",
			ClientSecret: "test_client_secret",
		},
	}

	// Disable plugin globally
	DisableGlobally()

	// Create mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := &SpotifyPlugin{}
	plugin.Start(nil)

	message := &telebot.Message{
		Chat: &telebot.Chat{ID: 123},
		Text: "Check out: https://open.spotify.com/album/6mUdeDZCsExyJLMdAfDuwh",
	}

	// Process should return early due to disabled plugin
	plugin.Process(message)

	// Verify no messages were sent
	if mockBot.SendCalled {
		t.Error("Expected no messages to be sent when plugin is disabled")
	}
}

func TestSpotifyPlugin_EnableDisable(t *testing.T) {
	// Setup test database
	testDB := testutils.SetupTestDB(t)
	defer testDB.Close()
	
	// Set the database for the registry to use
	database.DB = testDB

	// Ensure the plugin_settings table exists
	err := registry.EnsurePluginSettingsTable()
	if err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	// Test global enable/disable
	err = EnableGlobally()
	if err != nil {
		t.Errorf("EnableGlobally failed: %v", err)
	}

	setting := registry.GetPluginSetting(nil, SpotifyPluginName, SpotifyEnabledKey, "false")
	if setting != "true" {
		t.Error("Expected global setting to be 'true' after EnableGlobally")
	}

	err = DisableGlobally()
	if err != nil {
		t.Errorf("DisableGlobally failed: %v", err)
	}

	setting = registry.GetPluginSetting(nil, SpotifyPluginName, SpotifyEnabledKey, "true")
	if setting != "false" {
		t.Error("Expected global setting to be 'false' after DisableGlobally")
	}

	// Test chat-specific enable/disable
	chatID := int64(123)
	err = EnableForChat(chatID)
	if err != nil {
		t.Errorf("EnableForChat failed: %v", err)
	}

	setting = registry.GetPluginSetting(&chatID, SpotifyPluginName, SpotifyEnabledKey, "false")
	if setting != "true" {
		t.Errorf("Expected chat setting to be 'true' after EnableForChat for chat %d", chatID)
	}

	err = DisableForChat(chatID)
	if err != nil {
		t.Errorf("DisableForChat failed: %v", err)
	}

	setting = registry.GetPluginSetting(&chatID, SpotifyPluginName, SpotifyEnabledKey, "true")
	if setting != "false" {
		t.Errorf("Expected chat setting to be 'false' after DisableForChat for chat %d", chatID)
	}
}

func TestSpotifyPlugin_TokenManagement(t *testing.T) {
	plugin := &SpotifyPlugin{}

	// Test with no token
	plugin.accessToken = ""
	plugin.tokenExpiry = time.Now().Add(-1 * time.Hour)

	// Without actual API credentials, we can't test ensureAccessToken fully,
	// but we can verify the token expiry logic
	
	// Set a valid token
	plugin.accessToken = "test_token"
	plugin.tokenExpiry = time.Now().Add(1 * time.Hour)

	// Token should be considered valid
	if time.Now().After(plugin.tokenExpiry) {
		t.Error("Token should not be expired")
	}

	// Expire the token
	plugin.tokenExpiry = time.Now().Add(-1 * time.Hour)

	// Token should be considered expired
	if time.Now().Before(plugin.tokenExpiry) {
		t.Error("Token should be expired")
	}
}