package birthdays

import (
	"database/sql"
	"log"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bearbin/go-age"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

type BirthdaysPlugin struct {
}

var rng *rand.Rand

type birthdayConfig struct {
	chatID    int64
	birthdays map[string]time.Time
}

var birthdayConfigs []birthdayConfig

// Add a variable to allow mocking time.Now() in tests
var timeNow = time.Now

// Add a variable to allow mocking owner lookup in tests.
var isBotOwner = messageSenderIsBotOwner

func init() {
	registry.RegisterPlugin(&BirthdaysPlugin{})
}

func (p *BirthdaysPlugin) Start(interface{}) {
	rng = rand.New(rand.NewSource(timeNow().UnixNano()))

	if database.DB == nil {
		log.Printf("[birthdays] Database is not initialized, skipping birthday load")
		birthdayConfigs = make([]birthdayConfig, 0)
		return
	}

	if err := migrateConfigBirthdays(); err != nil {
		log.Printf("[birthdays] Error migrating birthdays from config: %v", err)
	}

	if err := reloadBirthdayConfigs(); err != nil {
		log.Printf("[birthdays] Error loading birthdays from DB: %v", err)
		birthdayConfigs = make([]birthdayConfig, 0)
	}
}

func (p *BirthdaysPlugin) Process(message *telebot.Message) {
	checkTodaysBirthdays(message)
	handleBirthdayCommand(message)
}

func migrateConfigBirthdays() error {
	loc := registry.Config.TimeLoc
	if loc == nil {
		loc = time.Local
	}

	for _, config := range registry.Config.Birthdays {
		for username, birthday := range config.Users {
			t, err := time.ParseInLocation("2006-01-02", birthday, loc)
			if err != nil {
				log.Printf("[birthdays] Skipping invalid birthday for chat %d user %s: %q", config.ChatID, username, birthday)
				continue
			}

			if err := saveBirthday(config.ChatID, username, t); err != nil {
				return err
			}
		}
	}

	return nil
}

func saveBirthday(chatID int64, username string, birthday time.Time) error {
	_, err := database.DB.Exec(`
		INSERT INTO birthdays (chat_id, username, birthday)
		VALUES (?, ?, ?)
		ON CONFLICT(chat_id, username) DO UPDATE SET birthday = excluded.birthday`,
		chatID, username, birthday.Format("2006-01-02"))
	return err
}

func deleteBirthday(chatID int64, username string) error {
	_, err := database.DB.Exec(`DELETE FROM birthdays WHERE chat_id = ? AND username = ?`, chatID, username)
	return err
}

func setBirthdayInMemory(chatID int64, username string, birthday time.Time) {
	for i := range birthdayConfigs {
		if birthdayConfigs[i].chatID != chatID {
			continue
		}
		if birthdayConfigs[i].birthdays == nil {
			birthdayConfigs[i].birthdays = make(map[string]time.Time)
		}
		birthdayConfigs[i].birthdays[username] = birthday
		return
	}

	birthdayConfigs = append(birthdayConfigs, birthdayConfig{
		chatID: chatID,
		birthdays: map[string]time.Time{
			username: birthday,
		},
	})
}

func deleteBirthdayInMemory(chatID int64, username string) {
	for i := range birthdayConfigs {
		if birthdayConfigs[i].chatID != chatID {
			continue
		}
		delete(birthdayConfigs[i].birthdays, username)
		return
	}
}

func reloadBirthdayConfigs() error {
	birthdayConfigs = make([]birthdayConfig, 0)

	rows, err := database.DB.Query(`
		SELECT chat_id, username, birthday
		FROM birthdays
		ORDER BY chat_id, username`)
	if err != nil {
		return err
	}
	defer rows.Close()

	loc := registry.Config.TimeLoc
	if loc == nil {
		loc = time.Local
	}

	configsByChat := make(map[int64]map[string]time.Time)
	for rows.Next() {
		var chatID int64
		var username string
		var birthday string
		if err := rows.Scan(&chatID, &username, &birthday); err != nil {
			return err
		}

		t, err := time.ParseInLocation("2006-01-02", birthday, loc)
		if err != nil {
			log.Printf("[birthdays] Skipping invalid DB birthday for chat %d user %s: %q", chatID, username, birthday)
			continue
		}

		if configsByChat[chatID] == nil {
			configsByChat[chatID] = make(map[string]time.Time)
		}
		configsByChat[chatID][username] = t
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for chatID, birthdays := range configsByChat {
		birthdayConfigs = append(birthdayConfigs, birthdayConfig{
			chatID:    chatID,
			birthdays: birthdays,
		})
	}

	return nil
}

func checkTodaysBirthdays(message *telebot.Message) {
	bot := registry.Bot
	loc := registry.Config.TimeLoc
	if loc == nil {
		loc = time.Local
	}

	cur := timeNow().In(loc)

	for _, config := range birthdayConfigs {
		if config.chatID != message.Chat.ID {
			continue
		}
		for username, birthday := range config.birthdays {
			if cur.Month() == birthday.Month() && cur.Day() == birthday.Day() && notMentioned(message.Chat.ID, username, cur.Year()) {
				age := strconv.Itoa(age.AgeAt(birthday, cur))
				bot.Send(message.Chat, "Hooray! 🎉 @"+username+" is turning "+age+"! 🎂", &telebot.SendOptions{})
			}
		}
	}
}

func handleBirthdayCommand(message *telebot.Message) {
	bot := registry.Bot
	loc := registry.Config.TimeLoc
	if loc == nil {
		loc = time.Local
	}

	if handleBirthdayAdminCommand(message, loc) {
		return
	}

	birthdayExp := regexp.MustCompile(`(?i)^\!(др|birthda(y|ys))$`)

	switch {
	case birthdayExp.MatchString(message.Text):
		cur := timeNow().In(loc)
		curDay := cur.YearDay()

		diff := time.Date(cur.Year(), time.December, 31, 0, 0, 0, 0, loc).YearDay()
		curDiff := diff
		curBirthday := ""
		curUsername := ""

		for _, config := range birthdayConfigs {
			if config.chatID != message.Chat.ID {
				continue
			}
			for username, birthday := range config.birthdays {
				birthdayDay := time.Date(cur.Year(), birthday.Month(), birthday.Day(), 0, 0, 0, 0, loc).YearDay()
				diff = birthdayDay - curDay
				if diff > 0 {
					if diff == curDiff {
						curUsername += ", @" + username
					} else if diff < curDiff {
						curDiff = diff
						curUsername = username
						curBirthday = birthday.Format("02.01")
					}
				}
			}
		}

		if curUsername != "" {
			bot.Send(message.Chat, "Prepare the 🎂 for @"+curUsername+" on "+curBirthday, &telebot.SendOptions{})
		} else {
			bot.Send(message.Chat, "No upcoming birthdays", &telebot.SendOptions{})
		}
	}
}

func handleBirthdayAdminCommand(message *telebot.Message, loc *time.Location) bool {
	if message == nil || message.Chat == nil {
		return false
	}

	parts := strings.Fields(message.Text)
	if len(parts) < 2 || len(parts) > 4 || !regexp.MustCompile(`(?i)^!birthday$`).MatchString(parts[0]) {
		return false
	}

	bot := registry.Bot
	targetChatID := message.Chat.ID
	argIndex := 1
	if parsedChatID, err := strconv.ParseInt(parts[argIndex], 10, 64); err == nil {
		targetChatID = parsedChatID
		argIndex++
	}

	if !isBotOwner(message) {
		bot.Send(message.Chat, "Only bot owner can manage birthdays", &telebot.SendOptions{})
		return true
	}

	if argIndex < len(parts) && strings.EqualFold(parts[argIndex], "list") {
		if argIndex != len(parts)-1 {
			bot.Send(message.Chat, birthdayUsage(), &telebot.SendOptions{})
			return true
		}
		text, err := formatBirthdayList(targetChatID)
		if err != nil {
			log.Printf("[birthdays] Error listing birthdays for chat %d: %v", targetChatID, err)
			bot.Send(message.Chat, "Failed to list birthdays", &telebot.SendOptions{})
			return true
		}
		bot.Send(message.Chat, text, &telebot.SendOptions{})
		return true
	}

	if argIndex+1 >= len(parts) || argIndex+2 != len(parts) || !strings.HasPrefix(parts[argIndex], "@") || len(parts[argIndex]) < 2 {
		bot.Send(message.Chat, birthdayUsage(), &telebot.SendOptions{})
		return true
	}
	username := strings.TrimPrefix(parts[argIndex], "@")
	value := parts[argIndex+1]

	if value == "-" {
		if err := deleteBirthday(targetChatID, username); err != nil {
			log.Printf("[birthdays] Error deleting birthday for chat %d user %s: %v", targetChatID, username, err)
			bot.Send(message.Chat, "Failed to delete birthday", &telebot.SendOptions{})
			return true
		}
		deleteBirthdayInMemory(targetChatID, username)
		bot.Send(message.Chat, "Deleted birthday for @"+username+" in chat "+strconv.FormatInt(targetChatID, 10), &telebot.SendOptions{})
		return true
	}

	birthday, err := time.ParseInLocation("2006-01-02", value, loc)
	if err != nil {
		bot.Send(message.Chat, "Invalid date. Use YYYY-MM-DD, for example: !birthday @username 1987-05-14", &telebot.SendOptions{})
		return true
	}

	if err := saveBirthday(targetChatID, username, birthday); err != nil {
		log.Printf("[birthdays] Error saving birthday for chat %d user %s: %v", targetChatID, username, err)
		bot.Send(message.Chat, "Failed to save birthday", &telebot.SendOptions{})
		return true
	}
	setBirthdayInMemory(targetChatID, username, birthday)
	bot.Send(message.Chat, "Saved birthday for @"+username+" on "+birthday.Format("2006-01-02")+" in chat "+strconv.FormatInt(targetChatID, 10), &telebot.SendOptions{})
	return true
}

func birthdayUsage() string {
	return "Usage: !birthday list, !birthday CHATID list, !birthday @username 1987-05-14, !birthday @username -, !birthday CHATID @username 1987-05-14, or !birthday CHATID @username -"
}

func formatBirthdayList(chatID int64) (string, error) {
	rows, err := database.DB.Query(`
		SELECT username, birthday
		FROM birthdays
		WHERE chat_id = ?
		ORDER BY substr(birthday, 6, 5), username`, chatID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	lines := []string{"Birthdays for chat " + strconv.FormatInt(chatID, 10) + ":"}
	for rows.Next() {
		var username string
		var birthday string
		if err := rows.Scan(&username, &birthday); err != nil {
			return "", err
		}
		lines = append(lines, "@"+username+" — "+birthday)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(lines) == 1 {
		lines = append(lines, "No birthdays configured")
	}
	return strings.Join(lines, "\n"), nil
}

func messageSenderIsBotOwner(message *telebot.Message) bool {
	if message == nil || message.Sender == nil {
		return false
	}
	return message.Sender.Username != "" && strings.EqualFold(message.Sender.Username, registry.Config.OwnerUsername)
}

func notMentioned(chatID int64, username string, year int) bool {
	var exists bool
	err := database.DB.QueryRow(
		"SELECT 1 FROM birthday_notifications WHERE chat_id = ? AND username = ? AND year = ?",
		chatID, username, year).Scan(&exists)

	if err != nil {
		if err == sql.ErrNoRows {
			// No record found, which means we haven't mentioned this user in this chat yet
			log.Printf("[birthdays] Notify %s in chat %d", username, chatID)

			_, err = database.DB.Exec(
				"INSERT INTO birthday_notifications (chat_id, username, year) VALUES (?, ?, ?)",
				chatID, username, year)
			if err != nil {
				log.Printf("[birthdays] Error saving birthday notification: %v", err)
				return false
			}

			return true
		} else {
			log.Printf("[birthdays] Error checking birthday notifications: %v", err)
			return false
		}
	}

	// Record found, which means we've already mentioned this user in this chat
	return false
}
