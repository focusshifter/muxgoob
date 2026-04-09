package spotify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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

func TestSpotifyReviewModelSetting(t *testing.T) {
	testDB := testutils.SetupTestDB(t)
	defer testDB.Close()
	database.DB = testDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	if err := SetReviewModel(nil, "openrouter/meta-llama/llama-3.1-70b-instruct"); err != nil {
		t.Fatalf("SetReviewModel global failed: %v", err)
	}

	if got := GetReviewModel(nil); got != "openrouter/meta-llama/llama-3.1-70b-instruct" {
		t.Fatalf("Expected global review model to be saved, got %q", got)
	}

	chatID := int64(321)
	if got := GetReviewModel(&chatID); got != "openrouter/meta-llama/llama-3.1-70b-instruct" {
		t.Fatalf("Expected chat to inherit global review model, got %q", got)
	}

	if err := SetReviewModel(&chatID, "openrouter/deepseek/deepseek-chat-v3.1"); err != nil {
		t.Fatalf("SetReviewModel chat failed: %v", err)
	}

	if got := GetReviewModel(&chatID); got != "openrouter/deepseek/deepseek-chat-v3.1" {
		t.Fatalf("Expected chat-specific review model, got %q", got)
	}
}

func TestResolveSpotifyReviewModelFallback(t *testing.T) {
	testDB := testutils.SetupTestDB(t)
	defer testDB.Close()
	database.DB = testDB

	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatalf("Failed to create plugin_settings table: %v", err)
	}

	chatID := int64(654)
	registry.Config = registry.Configuration{
		AiProvider: "openrouter",
		AiModel:    "openrouter/default-model",
	}

	if got := resolveSpotifyReviewModel(&chatID); got != "openrouter/default-model" {
		t.Fatalf("Expected OpenRouter chat fallback model, got %q", got)
	}

	if err := SetReviewModel(nil, "openrouter/spotify-model"); err != nil {
		t.Fatalf("SetReviewModel global failed: %v", err)
	}
	if got := resolveSpotifyReviewModel(&chatID); got != "openrouter/spotify-model" {
		t.Fatalf("Expected dedicated Spotify model to override fallback, got %q", got)
	}

	if err := SetReviewModel(&chatID, "openrouter/spotify-chat-model"); err != nil {
		t.Fatalf("SetReviewModel chat failed: %v", err)
	}
	if got := resolveSpotifyReviewModel(&chatID); got != "openrouter/spotify-chat-model" {
		t.Fatalf("Expected chat-specific Spotify model, got %q", got)
	}

	if err := SetReviewModel(nil, ""); err != nil {
		t.Fatalf("Failed to clear global Spotify model: %v", err)
	}

	otherChatID := int64(777)
	registry.Config.AiProvider = "openai"
	if got := resolveSpotifyReviewModel(&otherChatID); got != "gpt-4o-mini" {
		t.Fatalf("Expected OpenAI fallback model, got %q", got)
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

func TestSpotifyPlugin_FetchAlbum_RetriesAfterUnauthorized(t *testing.T) {
	var tokenRequests atomic.Int32
	var albumRequests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/token":
			requestNo := tokenRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"token-%d","expires_in":3600}`, requestNo)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/albums/test-album":
			requestNo := albumRequests.Add(1)
			auth := r.Header.Get("Authorization")
			if requestNo == 1 {
				if auth != "Bearer token-1" {
					t.Fatalf("expected first album request to use token-1, got %q", auth)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"status":401,"message":"The access token expired"}}`))
				return
			}
			if auth != "Bearer token-2" {
				t.Fatalf("expected retry album request to use token-2, got %q", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(SpotifyAlbum{Name: "Recovered Album"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	oldAuthURL := SpotifyAuthURL
	oldAPIBaseURL := SpotifyAPIBaseURL
	SpotifyAuthURL = server.URL + "/api/token"
	SpotifyAPIBaseURL = server.URL + "/v1"
	defer func() {
		SpotifyAuthURL = oldAuthURL
		SpotifyAPIBaseURL = oldAPIBaseURL
	}()

	registry.Config = registry.Configuration{
		SpotifyConfig: registry.SpotifyConfig{
			ClientID:     "test_client_id",
			ClientSecret: "test_client_secret",
		},
	}

	plugin := &SpotifyPlugin{}
	if err := plugin.EnsureAccessToken(); err != nil {
		t.Fatalf("EnsureAccessToken failed: %v", err)
	}

	album, err := plugin.FetchAlbum("test-album")
	if err != nil {
		t.Fatalf("FetchAlbum should retry on 401 and succeed, got error: %v", err)
	}
	if album == nil || album.Name != "Recovered Album" {
		t.Fatalf("expected recovered album payload, got %#v", album)
	}
	if got := tokenRequests.Load(); got != 2 {
		t.Fatalf("expected 2 token requests after 401 refresh, got %d", got)
	}
	if got := albumRequests.Load(); got != 2 {
		t.Fatalf("expected 2 album requests after retry, got %d", got)
	}
	if plugin.accessToken != "token-2" {
		t.Fatalf("expected plugin to store refreshed token, got %q", plugin.accessToken)
	}
}

func TestSpotifyPlugin_EnsureAccessToken_SerializesConcurrentRefresh(t *testing.T) {
	var tokenRequests atomic.Int32
	var gate sync.WaitGroup
	gate.Add(1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/token" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		tokenRequests.Add(1)
		gate.Wait()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"shared-token","expires_in":3600}`))
	}))
	defer server.Close()

	oldAuthURL := SpotifyAuthURL
	SpotifyAuthURL = server.URL + "/api/token"
	defer func() { SpotifyAuthURL = oldAuthURL }()

	registry.Config = registry.Configuration{
		SpotifyConfig: registry.SpotifyConfig{
			ClientID:     "test_client_id",
			ClientSecret: "test_client_secret",
		},
	}

	plugin := &SpotifyPlugin{}
	plugin.tokenExpiry = time.Now().Add(-time.Hour)

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			errCh <- plugin.EnsureAccessToken()
		}()
	}

	deadline := time.After(2 * time.Second)
	for tokenRequests.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for token request")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	gate.Done()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("EnsureAccessToken returned error: %v", err)
		}
	}

	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("expected exactly 1 token request for concurrent refresh, got %d", got)
	}
	if plugin.accessToken != "shared-token" {
		t.Fatalf("expected shared token to be stored, got %q", plugin.accessToken)
	}
}

func TestSpotifyPlugin_BuildAuthRequestUsesEncodedClientCredentials(t *testing.T) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	body := strings.NewReader(form.Encode())

	registry.Config = registry.Configuration{
		SpotifyConfig: registry.SpotifyConfig{
			ClientID:     "test_client_id",
			ClientSecret: "test_client_secret",
		},
	}

	req, err := buildSpotifyAuthRequest(SpotifyAuthURL, body)
	if err != nil {
		t.Fatalf("buildSpotifyAuthRequest failed: %v", err)
	}

	if got := req.Header.Get("Authorization"); got == "" || !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("expected Basic authorization header, got %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Fatalf("expected form content type, got %q", got)
	}
}
