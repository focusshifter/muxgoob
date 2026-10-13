package spotify

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	"github.com/tucnak/telebot"
)

func TestSpotifyPlugin_Process_RegenerateDoesNotSendPreview(t *testing.T) {
	oldDB, oldConfig, oldBot := database.DB, registry.Config, registry.Bot
	oldAuthURL, oldAPIURL := SpotifyAuthURL, SpotifyAPIBaseURL
	db := testutils.SetupTestDB(t)
	database.DB = db
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.DB, registry.Config, registry.Bot = oldDB, oldConfig, oldBot
		SpotifyAuthURL, SpotifyAPIBaseURL = oldAuthURL, oldAPIURL
		db.Close()
	})
	registry.Config = registry.Configuration{
		SpotifyConfig: registry.SpotifyConfig{ClientID: "test-client", ClientSecret: "test-secret"},
	}
	if err := registry.SetPluginSetting(nil, SpotifyPluginName, SpotifyReviewEnabledKey, "false"); err != nil {
		t.Fatal(err)
	}

	var art bytes.Buffer
	if err := jpeg.Encode(&art, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.URL.Path == "/token":
			fmt.Fprint(w, `{"access_token":"test-token","expires_in":3600}`)
		case strings.HasPrefix(r.URL.Path, "/albums/"):
			fmt.Fprintf(w, `{"name":"Test Album","release_date":"2026-01-01","artists":[{"name":"Test Artist"}],"images":[{"url":%q,"height":32,"width":32}]}`, server.URL+"/cover")
		case strings.HasPrefix(r.URL.Path, "/tracks/"):
			fmt.Fprintf(w, `{"name":"Test Track","artists":[{"name":"Test Artist"}],"album":{"release_date":"2026-01-01","images":[{"url":%q,"height":32,"width":32}]}}`, server.URL+"/cover")
		case r.URL.Path == "/cover":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(art.Bytes())
		default:
			http.NotFound(w, r)
		}
	})
	server.Start()
	defer server.Close()
	SpotifyAuthURL, SpotifyAPIBaseURL = server.URL+"/token", server.URL

	const albumURL = "https://open.spotify.com/album/3wiGXFOSEkkNOtxBJJG56Q"
	const trackURL = "https://open.spotify.com/track/1234567890abcdefghijkl"
	cases := []struct {
		name     string
		text     string
		entities []telebot.MessageEntity
		preview  bool
	}{
		{name: "regenerate album URL", text: "!spotify regenerate " + albumURL},
		{name: "regenerate track URL", text: "!spotify regenerate " + trackURL},
		{name: "regenerate extra spaces and query", text: "!spotify   regenerate " + albumURL + "?si=test"},
		{name: "regenerate hidden URL", text: "!spotify regenerate album", entities: []telebot.MessageEntity{{Type: telebot.EntityTextLink, URL: albumURL}}},
		{name: "ordinary album URL", text: albumURL, preview: true},
		{name: "ordinary track URL", text: trackURL, preview: true},
		{name: "ordinary hidden album URL", text: "album", entities: []telebot.MessageEntity{{Type: telebot.EntityTextLink, URL: albumURL}}, preview: true},
		{name: "command mentioned in prose", text: "Try !spotify regenerate " + albumURL, preview: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests.Store(0)
			bot := &testutils.MockBotWrapper{}
			registry.SetTestBot(bot)
			plugin := &SpotifyPlugin{}
			plugin.Start(nil)
			plugin.Process(&telebot.Message{Chat: &telebot.Chat{ID: 123}, Text: tc.text, Entities: tc.entities})
			if !tc.preview {
				if got := requests.Load(); got != 0 {
					t.Errorf("regenerate preview path made %d HTTP requests, want zero", got)
				}
				if bot.SendCalled {
					t.Errorf("regenerate preview path sent %T, want no message", bot.SendWhat)
				}
				return
			}
			if !bot.SendCalled {
				t.Fatal("ordinary Spotify link did not send its preview")
			}
			if _, ok := bot.SendWhat.(*telebot.Photo); !ok {
				t.Fatalf("ordinary Spotify link sent %T, want photo", bot.SendWhat)
			}
		})
	}
}
