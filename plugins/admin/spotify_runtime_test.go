package admin

import (
	"reflect"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/spotify"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	"github.com/tucnak/telebot"
)

func TestSpotifyRuntimeCommands(t *testing.T) {
	oldConfig, oldDB, oldBot := registry.Config, database.DB, registry.Bot
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() { registry.Config = oldConfig; database.DB = oldDB; registry.Bot = oldBot; db.Close() })
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	registry.Config.OwnerUsername = "owner"
	registry.Config.SpotifyConfig.ClientID = "test"
	registry.Config.SpotifyConfig.ClientSecret = "test"
	bot := &testutils.MockBotWrapper{}
	registry.SetTestBot(bot)
	p := &AdminPlugin{}
	send := func(text, user string) {
		p.Process(&telebot.Message{Text: text, Sender: &telebot.User{Username: user}, Chat: &telebot.Chat{ID: 1, Type: telebot.ChatPrivate}})
	}
	send("!spotify model provider/a provider/b:free provider/c", "owner")
	got := spotify.GetReviewSettings(nil)
	if got.Mode != "ensemble" || !reflect.DeepEqual(got.Models, []string{"provider/a", "provider/b:free", "provider/c"}) {
		t.Fatalf("global ensemble %+v response=%v", got, bot.SendWhat)
	}
	chat := int64(-100456)
	send("!spotify model chat/a chat/b -100456", "owner")
	got = spotify.GetReviewSettings(&chat)
	if !reflect.DeepEqual(got.Models, []string{"chat/a", "chat/b"}) {
		t.Fatal(got)
	}
	send("!spotify judge chat/judge:suffix -100456", "owner")
	send("!spotify model chat/single -100456", "owner")
	got = spotify.GetReviewSettings(&chat)
	if got.Mode != "single" || !reflect.DeepEqual(got.Models, []string{"chat/single"}) || got.JudgeModel != "chat/judge:suffix" {
		t.Fatal(got)
	}
	send("!spotify status -100456", "owner")
	response, _ := bot.SendWhat.(string)
	for _, want := range []string{"single", "chat/single", "chat/judge:suffix", "-100456"} {
		if !strings.Contains(response, want) {
			t.Errorf("status missing %q: %s", want, response)
		}
	}
	for _, command := range []string{"!spotify model a b c d", "!spotify model a a", "!spotify model", "!spotify judge", "!spotify judge a b", "!spotify judge a 999999999999999999999999"} {
		send(command, "owner")
		if now := spotify.GetReviewSettings(nil); !reflect.DeepEqual(now, spotify.ReviewSettings{Mode: "ensemble", Models: []string{"provider/a", "provider/b:free", "provider/c"}, JudgeModel: gotDefaultJudge()}) {
			t.Fatalf("malformed %q changed global %+v", command, now)
		}
	}
	send("!spotify model unauthorized", "not_owner")
	send("!spotify judge unauthorized -100456", "not_owner")
	if now := spotify.GetReviewSettings(&chat); !reflect.DeepEqual(now, got) {
		t.Fatalf("unauthorized mutation %+v", now)
	}
	send("!spotify model one/model", "owner")
	if now := spotify.GetReviewSettings(nil); now.Mode != "single" || spotify.GetReviewModel(nil) != "one/model" {
		t.Fatal(now)
	}
}

func gotDefaultJudge() string {
	if registry.Config.SpotifyReviewJudgeModel != "" {
		return registry.Config.SpotifyReviewJudgeModel
	}
	return "openai/gpt-6.1-sol"
}
