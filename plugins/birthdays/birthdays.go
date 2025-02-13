package birthdays

import (
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
var birthdays map[string]time.Time

func init() {
	registry.RegisterPlugin(&BirthdaysPlugin{})
}

func (p *BirthdaysPlugin) Start(interface{}) {
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))

	birthdays = make(map[string]time.Time)

	loc := registry.Config.TimeLoc

	for username, birthday := range registry.Config.Birthdays {
		t, _ := time.ParseInLocation("2006-01-02", birthday, loc)
		birthdays[username] = t
	}
}

func (p *BirthdaysPlugin) Process(message *telebot.Message) {
	todaysBirthday(message)
	nextBirthday(message)
}

func nextBirthday(message *telebot.Message) {
	bot := registry.Bot
	loc := registry.Config.TimeLoc

	birthdayExp := regexp.MustCompile(`(?i)^\!(др|birthda(y|ys))$`)

	switch {
	case birthdayExp.MatchString(message.Text):
		cur := time.Now().In(loc)
		curDay := cur.YearDay()

		diff := time.Date(cur.Year(), time.December, 31, 0, 0, 0, 0, time.Local).YearDay()
		curDiff := diff
		curBirthday := ""
		curUsername := ""

		for username, birthday := range birthdays {
			birthdayDay := time.Date(cur.Year(), birthday.Month(), birthday.Day(), 0, 0, 0, 0, time.Local).YearDay()
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

		if curUsername != "" {
			bot.Send(message.Chat, "Prepare the 🎂 for @"+curUsername+" on "+curBirthday, &telebot.SendOptions{})
		} else {
			bot.Send(message.Chat, "No upcoming birthdays", &telebot.SendOptions{})
		}
	}
}

func todaysBirthday(message *telebot.Message) {
	bot := registry.Bot
	loc := registry.Config.TimeLoc

	cur := time.Now().In(loc)

	for username, birthday := range birthdays {
		if cur.Month() == birthday.Month() && cur.Day() == birthday.Day() && notMentioned(username, cur.Year(), message) {
			age := strconv.Itoa(age.AgeAt(birthday, cur))
			bot.Send(message.Chat, "Hooray! 🎉 @"+username+" is turning "+age+"! 🎂", &telebot.SendOptions{})
		}
	}
}

func notMentioned(username string, year int, message *telebot.Message) bool {
	var exists bool
	err := database.DB.QueryRow(
		"SELECT 1 FROM birthday_notifications WHERE username = ? AND year = ?",
		username, year).Scan(&exists)

	if err != nil {
		log.Printf("Error checking birthday notifications: %v", err)
		return false
	}

	if exists {
		return false
	}

	log.Println("Birthday: notify " + username)

	_, err = database.DB.Exec(
		"INSERT INTO birthday_notifications (username, year) VALUES (?, ?)",
		username, year)
	if err != nil {
		log.Printf("Error saving birthday notification: %v", err)
		return false
	}

	return true
}
