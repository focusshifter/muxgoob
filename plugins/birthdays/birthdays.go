package birthdays

import (
	"database/sql"
	"log"
	"math/rand"
	"regexp"
	"strconv"
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
