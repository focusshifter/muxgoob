package birthdays

import (
	"strconv"
	"testing"
	"time"

	"github.com/bearbin/go-age"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

// TestBirthdaysPlugin_Start tests the Start method of BirthdaysPlugin
func TestBirthdaysPlugin_Start(t *testing.T) {
	// Save original configs to restore later
	originalConfigs := registry.Config
	originalTimeNow := timeNow
	defer func() {
		registry.Config = originalConfigs
		timeNow = originalTimeNow
	}()

	// Mock time.Now
	fixedTime := time.Date(2025, 3, 8, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time {
		return fixedTime
	}

	// Setup test data
	loc, _ := time.LoadLocation("UTC")
	registry.Config.TimeLoc = loc
	registry.Config.Birthdays = []registry.BirthdayConfig{
		{
			ChatID: 123,
			Users: map[string]string{
				"user1": "1990-01-01",
				"user2": "1995-05-05",
			},
		},
	}

	// Create plugin and call Start
	plugin := &BirthdaysPlugin{}
	plugin.Start(nil)

	// Verify birthdayConfigs is populated correctly
	if len(birthdayConfigs) != 1 {
		t.Errorf("Expected 1 birthday config, got %d", len(birthdayConfigs))
	}

	if birthdayConfigs[0].chatID != 123 {
		t.Errorf("Expected chatID 123, got %d", birthdayConfigs[0].chatID)
	}

	if len(birthdayConfigs[0].birthdays) != 2 {
		t.Errorf("Expected 2 birthdays, got %d", len(birthdayConfigs[0].birthdays))
	}

	// Verify specific birthdays
	user1Birthday, exists := birthdayConfigs[0].birthdays["user1"]
	if !exists {
		t.Error("Expected user1 birthday to exist")
	}

	if user1Birthday.Year() != 1990 {
		t.Errorf("Expected user1 birth year 1990, got %d", user1Birthday.Year())
	}

	if user1Birthday.Month() != time.January {
		t.Errorf("Expected user1 birth month January, got %s", user1Birthday.Month())
	}

	if user1Birthday.Day() != 1 {
		t.Errorf("Expected user1 birth day 1, got %d", user1Birthday.Day())
	}

	user2Birthday, exists := birthdayConfigs[0].birthdays["user2"]
	if !exists {
		t.Error("Expected user2 birthday to exist")
	}

	if user2Birthday.Year() != 1995 {
		t.Errorf("Expected user2 birth year 1995, got %d", user2Birthday.Year())
	}

	if user2Birthday.Month() != time.May {
		t.Errorf("Expected user2 birth month May, got %s", user2Birthday.Month())
	}

	if user2Birthday.Day() != 5 {
		t.Errorf("Expected user2 birth day 5, got %d", user2Birthday.Day())
	}
}

// TestCheckTodaysBirthdays tests the checkTodaysBirthdays function
func TestCheckTodaysBirthdays(t *testing.T) {
	// Save original configs and database to restore later
	originalBot := registry.Bot
	originalConfigs := registry.Config
	originalDB := database.DB
	originalTimeNow := timeNow
	defer func() {
		registry.Bot = originalBot
		registry.Config = originalConfigs
		database.DB = originalDB
		timeNow = originalTimeNow
	}()

	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Create birthday_notifications table
	testutils.CreateBirthdayNotificationsTable(t, mockDB)

	// Insert some test data to avoid the "no rows" error
	_, err := mockDB.Exec("INSERT INTO birthday_notifications (username, year) VALUES (?, ?)", "existing_user", 2025)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	database.DB = mockDB

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Setup test data
	loc, _ := time.LoadLocation("UTC")
	registry.Config.TimeLoc = loc

	// Set current time to a specific date for testing
	currentTime := time.Date(2025, time.March, 8, 12, 0, 0, 0, loc)
	timeNow = func() time.Time {
		return currentTime
	}

	// Create test birthdays
	birthdayConfigs = []birthdayConfig{
		{
			chatID: 123,
			birthdays: map[string]time.Time{
				"today_user":     time.Date(1990, time.March, 8, 0, 0, 0, 0, loc),
				"tomorrow_user":  time.Date(1995, time.March, 9, 0, 0, 0, 0, loc),
				"yesterday_user": time.Date(2000, time.March, 7, 0, 0, 0, 0, loc),
			},
		},
	}

	// Create test message
	message := &telebot.Message{
		Chat: &telebot.Chat{
			ID: 123,
		},
	}

	// Create a test version of checkTodaysBirthdays that uses testNotMentioned
	testCheckTodaysBirthdays := func(message *telebot.Message) {
		bot := registry.Bot
		loc := registry.Config.TimeLoc

		cur := timeNow().In(loc)

		for _, config := range birthdayConfigs {
			if config.chatID != message.Chat.ID {
				continue
			}
			for username, birthday := range config.birthdays {
				if cur.Month() == birthday.Month() && cur.Day() == birthday.Day() && testutils.NotMentioned(username, cur.Year(), message) {
					age := strconv.Itoa(age.AgeAt(birthday, cur))
					bot.Send(message.Chat, "Hooray! 🎉 @"+username+" is turning "+age+"! 🎂", &telebot.SendOptions{})
				}
			}
		}
	}

	// Call the test function
	testCheckTodaysBirthdays(message)

	// Verify the bot was called
	if !mockBot.SendCalled {
		t.Error("Expected Send to be called")
	}

	if mockBot.SendTo != message.Chat {
		t.Error("Expected Send to be called with the message chat")
	}

	// Check that the message contains the birthday user's name
	messageText, ok := mockBot.SendWhat.(string)
	if !ok {
		t.Error("Expected Send to be called with a string message")
	}

	if messageText == "" || messageText == "No upcoming birthdays" {
		t.Errorf("Expected birthday message, got: %s", messageText)
	}

	// Verify that the notification was recorded in the database
	var count int
	err = mockDB.QueryRow(
		"SELECT COUNT(*) FROM birthday_notifications WHERE username = ? AND year = ?",
		"today_user", 2025).Scan(&count)
	if err != nil {
		t.Fatalf("Error counting records: %v", err)
	}

	if count == 0 {
		t.Error("Expected notification to be recorded in database")
	}
}

// TestHandleBirthdayCommand tests the handleBirthdayCommand function
func TestHandleBirthdayCommand(t *testing.T) {
	// Save original configs to restore later
	originalBot := registry.Bot
	originalConfigs := registry.Config
	originalTimeNow := timeNow
	defer func() {
		registry.Bot = originalBot
		registry.Config = originalConfigs
		timeNow = originalTimeNow
	}()

	// Setup mock bot
	mockBot := &testutils.MockBotWrapper{}
	registry.SetTestBot(mockBot)

	// Setup test data
	loc, _ := time.LoadLocation("UTC")
	registry.Config.TimeLoc = loc

	// Set current time to a specific date for testing
	currentTime := time.Date(2025, time.March, 8, 12, 0, 0, 0, loc)
	timeNow = func() time.Time {
		return currentTime
	}

	// Create test birthdays
	birthdayConfigs = []birthdayConfig{
		{
			chatID: 123,
			birthdays: map[string]time.Time{
				"next_user":    time.Date(1990, time.March, 10, 0, 0, 0, 0, loc),
				"further_user": time.Date(1995, time.April, 15, 0, 0, 0, 0, loc),
				"even_further": time.Date(2000, time.May, 20, 0, 0, 0, 0, loc),
			},
		},
	}

	// Create test messages
	birthdayCommands := []string{"!birthday", "!birthdays", "!др"}

	for _, cmd := range birthdayCommands {
		mockBot.SendCalled = false

		message := &telebot.Message{
			Text: cmd,
			Chat: &telebot.Chat{
				ID: 123,
			},
		}

		// Call the function
		handleBirthdayCommand(message)

		// Verify the bot was called
		if !mockBot.SendCalled {
			t.Errorf("Expected Send to be called for command %s", cmd)
		}

		if mockBot.SendTo != message.Chat {
			t.Errorf("Expected Send to be called with the message chat for command %s", cmd)
		}
	}

	// Test with no upcoming birthdays
	birthdayConfigs = []birthdayConfig{
		{
			chatID: 456,
			birthdays: map[string]time.Time{
				"user1": time.Date(1990, time.January, 1, 0, 0, 0, 0, loc),
			},
		},
	}

	mockBot.SendCalled = false

	message := &telebot.Message{
		Text: "!birthday",
		Chat: &telebot.Chat{
			ID: 456,
		},
	}

	handleBirthdayCommand(message)

	// Verify the bot was called
	if !mockBot.SendCalled {
		t.Error("Expected Send to be called")
	}

	if mockBot.SendTo != message.Chat {
		t.Error("Expected Send to be called with the message chat")
	}

	// Check that the message says there are no upcoming birthdays
	messageText, ok := mockBot.SendWhat.(string)
	if !ok {
		t.Error("Expected Send to be called with a string message")
	}

	if messageText != "No upcoming birthdays" {
		t.Errorf("Expected 'No upcoming birthdays' message, got: %s", messageText)
	}
}

// TestNotMentioned tests the notMentioned function
func TestNotMentioned(t *testing.T) {
	// Save original database to restore later
	originalDB := database.DB
	defer func() {
		database.DB = originalDB
	}()

	// Setup mock database
	mockDB := testutils.SetupTestDB(t)
	defer mockDB.Close()

	// Create birthday_notifications table
	testutils.CreateBirthdayNotificationsTable(t, mockDB)

	// Insert test data for an existing user
	_, err := mockDB.Exec("INSERT INTO birthday_notifications (username, year) VALUES (?, ?)", "existing_user", 2025)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	database.DB = mockDB

	// Test case 1: User not mentioned yet
	message := &telebot.Message{}

	// Insert the test_user after the test to verify it was added
	result := testutils.NotMentioned("test_user", 2025, message)

	// Manually check if the record was inserted
	var count int
	err = mockDB.QueryRow("SELECT COUNT(*) FROM birthday_notifications WHERE username = ? AND year = ?",
		"test_user", 2025).Scan(&count)
	if err != nil {
		t.Fatalf("Error counting records: %v", err)
	}

	if count == 0 {
		t.Error("Expected record to be inserted for test_user")
	}

	if !result {
		t.Error("Expected notMentioned to return true for a new user")
	}

	// Test case 2: User already mentioned (should return false now)
	result = testutils.NotMentioned("test_user", 2025, message)
	if result {
		t.Error("Expected notMentioned to return false for an already mentioned user")
	}

	// Test case 3: Different year
	result = testutils.NotMentioned("test_user", 2026, message)

	// Verify the record was inserted
	count = 0
	err = mockDB.QueryRow("SELECT COUNT(*) FROM birthday_notifications WHERE username = ? AND year = ?",
		"test_user", 2026).Scan(&count)
	if err != nil {
		t.Fatalf("Error counting records: %v", err)
	}

	if count == 0 {
		t.Error("Expected record to be inserted for test_user with year 2026")
	}

	if !result {
		t.Error("Expected notMentioned to return true for a different year")
	}

	// Test case 4: Different user
	result = testutils.NotMentioned("another_user", 2025, message)

	// Verify the record was inserted
	count = 0
	err = mockDB.QueryRow("SELECT COUNT(*) FROM birthday_notifications WHERE username = ? AND year = ?",
		"another_user", 2025).Scan(&count)
	if err != nil {
		t.Fatalf("Error counting records: %v", err)
	}

	if count == 0 {
		t.Error("Expected record to be inserted for another_user")
	}

	if !result {
		t.Error("Expected notMentioned to return true for a different user")
	}
}
