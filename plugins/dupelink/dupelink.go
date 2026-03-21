package dupelink

import (
	"encoding/json"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

type DupeLinkPlugin struct {
}

func init() {
	registry.RegisterPlugin(&DupeLinkPlugin{})
}

func (p *DupeLinkPlugin) Start(interface{}) {}

func (p *DupeLinkPlugin) Process(message *telebot.Message) {
	messageURLs := getURLs(message)
	var validURLs []string

	newURL := func(currentURL string, validURLs []string) bool {
		for _, existingURL := range validURLs {
			if existingURL == currentURL {
				return false
			}
		}

		return true
	}

	for _, messageURL := range messageURLs {
		parsedURL, err := url.Parse(messageURL)

		if err != nil {
			continue
		}

		// Custom logic for some of the domains
		// For example, for open.spotify.com we remove all parameters
		if parsedURL.Hostname() == "open.spotify.com" {
			parsedURL.RawQuery = ""
		}

		// Normalize YouTube URLs to extract video ID
		currentURL := normalizeURL(parsedURL)

		for _, ignoredHostname := range registry.Config.DupeIgnoredDomains {
			if parsedURL.Hostname() == ignoredHostname {
				log.Printf("[dupelink] Skipping %s because %s is blacklisted", currentURL, ignoredHostname)
				return
			}
		}

		if newURL(currentURL, validURLs) {
			validURLs = append(validURLs, currentURL)
			reactToURL(currentURL, message)
		}
	}
}

func getURLs(message *telebot.Message) []string {
	var urls []string

	for _, entity := range message.Entities {
		if entity.Type == "url" {
			urls = append(urls, string([]rune(message.Text)[entity.Offset:(entity.Offset+entity.Length)]))
		}
	}

	return urls
}

// normalizeURL normalizes URLs for duplicate detection
// Handles special cases like YouTube URLs with different formats
func normalizeURL(parsedURL *url.URL) string {
	hostname := parsedURL.Hostname()

	// Normalize YouTube URLs
	if isYouTubeDomain(hostname) {
		videoID := extractYouTubeVideoID(parsedURL)
		if videoID != "" {
			// Normalize to consistent format: youtube.com/watch?v=VIDEO_ID
			return "youtube.com/watch?v=" + videoID
		}
	}

	// Default: hostname + request URI
	return hostname + parsedURL.RequestURI()
}

// isYouTubeDomain checks if the hostname is a YouTube domain
func isYouTubeDomain(hostname string) bool {
	hostname = strings.ToLower(hostname)
	return hostname == "youtube.com" ||
		hostname == "www.youtube.com" ||
		hostname == "m.youtube.com" ||
		hostname == "youtu.be"
}

// extractYouTubeVideoID extracts the video ID from various YouTube URL formats
func extractYouTubeVideoID(parsedURL *url.URL) string {
	hostname := strings.ToLower(parsedURL.Hostname())

	// Handle youtu.be/VIDEO_ID format
	if hostname == "youtu.be" {
		path := strings.TrimPrefix(parsedURL.Path, "/")
		// Remove any query parameters from the path (shouldn't happen, but be safe)
		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
		if path != "" {
			return path
		}
	}

	// Handle youtube.com/watch?v=VIDEO_ID format
	if hostname == "youtube.com" || hostname == "www.youtube.com" || hostname == "m.youtube.com" {
		videoID := parsedURL.Query().Get("v")
		if videoID != "" {
			return videoID
		}
		// Also handle /embed/VIDEO_ID and /v/VIDEO_ID formats
		path := strings.TrimPrefix(parsedURL.Path, "/")
		if strings.HasPrefix(path, "embed/") {
			return strings.TrimPrefix(path, "embed/")
		}
		if strings.HasPrefix(path, "v/") {
			return strings.TrimPrefix(path, "v/")
		}
	}

	return ""
}

func reactToURL(currentURL string, message *telebot.Message) {
	bot := registry.Bot

	// Try to find existing link
	var firstName, lastName string
	var unixtime int64

	err := database.DB.QueryRow(
		`SELECT u.first_name, u.last_name, d.unixtime 
		FROM dupe_links d 
		JOIN users u ON d.sender_id = u.id 
		WHERE d.url = ? AND d.chat_id = ? 
		LIMIT 1`,
		currentURL, message.Chat.ID).Scan(&firstName, &lastName, &unixtime)

	if err == nil {
		log.Printf("[dupelink] Found dupe, reporting: %s", currentURL)
		formattedTime := time.Unix(unixtime, 0).Format(time.RFC1123)
		formattedUser := firstName + " " + lastName
		bot.Send(message.Chat, "That was already posted on "+formattedTime+" by "+formattedUser,
			&telebot.SendOptions{ReplyTo: message})
	} else {
		log.Printf("[dupelink] Link not found, saving: %s", currentURL)

		// First, ensure user exists in the database
		userData, _ := json.Marshal(message.Sender)
		_, err = database.DB.Exec(
			"INSERT OR IGNORE INTO users (id, username, first_name, last_name, data) VALUES (?, ?, ?, ?, ?)",
			message.Sender.ID, message.Sender.Username, message.Sender.FirstName, message.Sender.LastName, string(userData))
		if err != nil {
			log.Printf("[dupelink] Error saving user: %v", err)
			return
		}

		// Then save the dupe link
		_, err = database.DB.Exec(
			"INSERT INTO dupe_links (url, message_id, sender_id, chat_id, unixtime) VALUES (?, ?, ?, ?, ?)",
			currentURL, message.ID, message.Sender.ID, message.Chat.ID, message.Unixtime)
		if err != nil {
			log.Printf("[dupelink] Error saving dupe link: %v", err)
		}
	}
}
