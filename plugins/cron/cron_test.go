package cron

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	"github.com/tucnak/telebot"
)

func TestCronCommandsAreOwnerOnlyAndPersistExactCommand(t *testing.T) {
	originalConfig := registry.Config
	originalBot := registry.Bot
	originalDB := database.DB
	defer func() {
		registry.Config = originalConfig
		registry.Bot = originalBot
		database.DB = originalDB
	}()

	db := testutils.SetupTestDB(t)
	defer db.Close()
	database.DB = db
	if _, err := db.Exec(`CREATE TABLE cron_jobs (
		chat_id INTEGER NOT NULL,
		alias TEXT NOT NULL,
		expression TEXT NOT NULL,
		command TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (chat_id, alias)
	)`); err != nil {
		t.Fatal(err)
	}

	registry.Config.OwnerUsername = "owner"
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	registry.Config.TimeLoc = loc
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	plugin := newPluginForTest()
	nonOwner := cronMessage("not-owner", "!cron add -100 \"0 9 * * *\" morning !version")
	plugin.Process(nonOwner)
	assertCronJobCount(t, db, 0)
	if mockBot.SendCalled {
		t.Fatal("non-owner must not receive a cron command response")
	}

	plugin.Process(cronMessage("owner", "!cron add -100 \"0 9 * * *\" morning !version"))
	var expression, command string
	if err := db.QueryRow(`SELECT expression, command FROM cron_jobs WHERE chat_id = ? AND alias = ?`, -100, "morning").Scan(&expression, &command); err != nil {
		t.Fatalf("expected persisted cron job: %v", err)
	}
	if expression != "0 9 * * *" || command != "!version" {
		t.Fatalf("stored job = (%q, %q), want exact expression and command", expression, command)
	}

	plugin.Process(cronMessage("owner", "!cron update -100 morning !prompt send morning briefing"))
	if err := db.QueryRow(`SELECT command FROM cron_jobs WHERE chat_id = ? AND alias = ?`, -100, "morning").Scan(&command); err != nil {
		t.Fatal(err)
	}
	if command != "!prompt send morning briefing" {
		t.Fatalf("updated command = %q", command)
	}

	plugin.Process(cronMessage("owner", "!cron reschedule -100 morning \"0 10 * * *\""))
	if err := db.QueryRow(`SELECT expression FROM cron_jobs WHERE chat_id = ? AND alias = ?`, -100, "morning").Scan(&expression); err != nil {
		t.Fatal(err)
	}
	if expression != "0 10 * * *" {
		t.Fatalf("rescheduled expression = %q", expression)
	}

	plugin.Process(cronMessage("owner", "!cron list -100"))
	listing, _ := mockBot.SendWhat.(string)
	if !strings.Contains(listing, "Cron jobs (Europe/Moscow):") ||
		!strings.Contains(listing, `-100 / morning — "0 10 * * *" → !prompt send morning briefing`) {
		t.Fatalf("cron listing = %q", listing)
	}

	plugin.Process(cronMessage("owner", "!cron remove -100 morning"))
	assertCronJobCount(t, db, 0)
}

func TestCronScheduleUsesConfiguredTimezone(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}

	schedule, err := parseSchedule("0 9 * * *", loc)
	if err != nil {
		t.Fatal(err)
	}

	next := schedule.Next(time.Date(2026, time.January, 1, 5, 59, 0, 0, time.UTC))
	want := time.Date(2026, time.January, 1, 6, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s (09:00 Europe/Moscow)", next, want)
	}
}

func TestCronRejectsMalformedInputAndInvalidSchedules(t *testing.T) {
	originalConfig := registry.Config
	originalBot := registry.Bot
	originalDB := database.DB
	defer func() {
		registry.Config = originalConfig
		registry.Bot = originalBot
		database.DB = originalDB
	}()

	db := testutils.SetupTestDB(t)
	defer db.Close()
	database.DB = db
	if _, err := db.Exec(`CREATE TABLE cron_jobs (chat_id INTEGER NOT NULL, alias TEXT NOT NULL, expression TEXT NOT NULL, command TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (chat_id, alias))`); err != nil {
		t.Fatal(err)
	}

	registry.Config.OwnerUsername = "owner"
	registry.Config.TimeLoc = time.UTC
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)
	plugin := newPluginForTest()

	plugin.Process(cronMessage("owner", "!cron add -100 \"not cron\" morning !version"))
	assertCronJobCount(t, db, 0)
	response, _ := mockBot.SendWhat.(string)
	if !strings.Contains(response, "Invalid cron expression") {
		t.Fatalf("response = %q, want invalid-expression error", response)
	}
}

func TestScheduledCommandDispatchesAsOwnerInTargetChat(t *testing.T) {
	originalConfig := registry.Config
	originalPlugins := registry.Plugins
	originalDB := database.DB
	defer func() {
		registry.Config = originalConfig
		registry.Plugins = originalPlugins
		database.DB = originalDB
	}()

	registry.Config.OwnerUsername = "owner"
	database.DB = nil
	received := make(chan *telebot.Message, 1)
	registry.Plugins = map[string]registry.MuxPlugin{"capture": capturePlugin{received: received}}

	dispatchScheduledCommand(cronJob{ChatID: -100, Alias: "morning", Command: "!version"})

	select {
	case message := <-received:
		if message.Chat == nil || message.Chat.ID != -100 {
			t.Fatalf("target chat = %#v, want -100", message.Chat)
		}
		if message.Sender == nil || message.Sender.Username != "owner" {
			t.Fatalf("scheduled sender = %#v, want owner", message.Sender)
		}
		if message.Text != "!version" {
			t.Fatalf("scheduled command = %q", message.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduled command was not dispatched")
	}
}

type capturePlugin struct {
	received chan<- *telebot.Message
}

func (p capturePlugin) Start(interface{}) {}

func (p capturePlugin) Process(message *telebot.Message) {
	p.received <- message
}

func cronMessage(username, text string) *telebot.Message {
	return &telebot.Message{
		Text:   text,
		Sender: &telebot.User{Username: username},
		Chat:   &telebot.Chat{ID: 1, Type: telebot.ChatPrivate},
	}
}

func assertCronJobCount(t *testing.T, db interface {
	QueryRow(string, ...interface{}) *sql.Row
}, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("cron job count = %d, want %d", count, want)
	}
}
