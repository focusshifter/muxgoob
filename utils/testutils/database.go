package testutils

import (
	"database/sql"
	"log"
	"testing"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
)

// SetupTestDB creates an in-memory SQLite database for testing
func SetupTestDB(t *testing.T) *sql.DB {
	mockDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open in-memory SQLite database: %v", err)
	}
	// Ensure all queries use the same connection for in-memory DBs.
	mockDB.SetMaxOpenConns(1)
	mockDB.SetMaxIdleConns(1)

	return mockDB
}

// CreateBirthdayNotificationsTable creates the birthday_notifications table in the test database
func CreateBirthdayNotificationsTable(t *testing.T, db *sql.DB) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS birthday_notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			year INTEGER,
			UNIQUE(username, year)
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create birthday_notifications table: %v", err)
	}
}

// NotMentioned is a test-friendly version of the notMentioned function
// that handles the sql.ErrNoRows case correctly
func NotMentioned(username string, year int, message *telebot.Message) bool {
	var exists bool
	err := database.DB.QueryRow(
		"SELECT 1 FROM birthday_notifications WHERE username = ? AND year = ?",
		username, year).Scan(&exists)

	if err != nil {
		if err == sql.ErrNoRows {
			// No record found, which means we haven't mentioned this user yet
			log.Printf("[birthdays] Notify %s", username)

			_, err = database.DB.Exec(
				"INSERT INTO birthday_notifications (username, year) VALUES (?, ?)",
				username, year)
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

	// Record found, which means we've already mentioned this user
	return false
}
