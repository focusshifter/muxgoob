package dupelink

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

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
	if handleBlacklistCommand(message) {
		return
	}

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

		ignored, err := isDomainIgnored(message.Chat.ID, parsedURL.Hostname())
		if err != nil {
			log.Printf("[dupelink] Failed to read ignored domains for chat %d; skipping duplicate check: %v", message.Chat.ID, err)
			continue
		}
		if ignored {
			log.Printf("[dupelink] Skipping %s because its domain is ignored in chat %d", currentURL, message.Chat.ID)
			continue
		}

		if newURL(currentURL, validURLs) {
			validURLs = append(validURLs, currentURL)
			reactToURL(currentURL, message)
		}
	}
}

func handleBlacklistCommand(message *telebot.Message) bool {
	if message == nil {
		return false
	}
	fields := strings.Fields(message.Text)
	if len(fields) < 2 || strings.ToLower(fields[0]) != "!dupelink" || strings.ToLower(fields[1]) != "blacklist" {
		return false
	}
	if message.Chat == nil || message.Sender == nil || registry.Config.OwnerUsername == "" || message.Sender.Username == "" || !strings.EqualFold(message.Sender.Username, registry.Config.OwnerUsername) {
		return true
	}

	usage := "Usage: !dupelink blacklist add <domain> | remove <domain> | list"
	if len(fields) < 3 {
		registry.Bot.Send(message.Chat, usage)
		return true
	}

	switch strings.ToLower(fields[2]) {
	case "add":
		if len(fields) != 4 {
			registry.Bot.Send(message.Chat, usage)
			return true
		}
		domain, err := canonicalDomain(fields[3])
		if err != nil {
			registry.Bot.Send(message.Chat, "Invalid domain: "+err.Error())
			return true
		}
		result, err := database.DB.Exec(`
			INSERT OR IGNORE INTO dupe_ignored_domains (chat_id, domain, created_at)
			VALUES (?, ?, ?)`, message.Chat.ID, domain, time.Now().Unix())
		if err != nil {
			log.Printf("[dupelink] Failed to add ignored domain: %v", err)
			registry.Bot.Send(message.Chat, "Failed to update duplicate-link blacklist.")
			return true
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			registry.Bot.Send(message.Chat, fmt.Sprintf("%s is already blacklisted for duplicate warnings in this chat.", domain))
			return true
		}
		registry.Bot.Send(message.Chat, fmt.Sprintf("Duplicate warnings disabled for %s in this chat.", domain))
	case "remove", "delete", "rm":
		if len(fields) != 4 {
			registry.Bot.Send(message.Chat, usage)
			return true
		}
		domain, err := canonicalDomain(fields[3])
		if err != nil {
			registry.Bot.Send(message.Chat, "Invalid domain: "+err.Error())
			return true
		}
		result, err := database.DB.Exec(`DELETE FROM dupe_ignored_domains WHERE chat_id = ? AND domain = ?`, message.Chat.ID, domain)
		if err != nil {
			log.Printf("[dupelink] Failed to remove ignored domain: %v", err)
			registry.Bot.Send(message.Chat, "Failed to update duplicate-link blacklist.")
			return true
		}
		if configuredDomainMatches(domain) {
			registry.Bot.Send(message.Chat, fmt.Sprintf("Removed the chat-specific entry for %s, but duplicate warnings remain disabled by the global configuration.", domain))
			return true
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			registry.Bot.Send(message.Chat, fmt.Sprintf("%s was not blacklisted for this chat.", domain))
			return true
		}
		registry.Bot.Send(message.Chat, fmt.Sprintf("Duplicate warnings re-enabled for %s in this chat.", domain))
	case "list":
		if len(fields) != 3 {
			registry.Bot.Send(message.Chat, usage)
			return true
		}
		localDomains, err := ignoredDomains(message.Chat.ID)
		if err != nil {
			log.Printf("[dupelink] Failed to list ignored domains: %v", err)
			registry.Bot.Send(message.Chat, "Failed to read duplicate-link blacklist.")
			return true
		}
		globalDomains := configuredDomains()
		if len(localDomains) == 0 && len(globalDomains) == 0 {
			registry.Bot.Send(message.Chat, "No domains are blacklisted for duplicate warnings in this chat.")
			return true
		}
		registry.Bot.Send(message.Chat, formatBlacklist(localDomains, globalDomains))
	default:
		registry.Bot.Send(message.Chat, usage)
	}
	return true
}

func canonicalDomain(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("domain is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	host = strings.TrimPrefix(host, "www.")
	if err := validateDomain(host); err != nil {
		return "", err
	}
	return host, nil
}

func validateDomain(host string) error {
	if host == "" {
		return fmt.Errorf("missing hostname")
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if len(host) > 253 || !strings.Contains(host, ".") {
		return fmt.Errorf("expected a fully qualified domain")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid domain")
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("invalid domain")
			}
		}
	}
	return nil
}

func ignoredDomains(chatID int64) ([]string, error) {
	rows, err := database.DB.Query(`SELECT domain FROM dupe_ignored_domains WHERE chat_id = ? ORDER BY domain`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

func configuredDomains() []string {
	seen := make(map[string]struct{})
	var domains []string
	for _, raw := range registry.Config.DupeIgnoredDomains {
		domain, err := canonicalDomain(raw)
		if err != nil {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

func configuredDomainMatches(hostname string) bool {
	for _, domain := range configuredDomains() {
		if domainMatches(hostname, domain) {
			return true
		}
	}
	return false
}

func formatBlacklist(localDomains, globalDomains []string) string {
	var sections []string
	if len(localDomains) > 0 {
		sections = append(sections, "This chat:\n- "+strings.Join(localDomains, "\n- "))
	}
	if len(globalDomains) > 0 {
		sections = append(sections, "Inherited from global config:\n- "+strings.Join(globalDomains, "\n- "))
	}
	text := "Duplicate-warning blacklist:\n" + strings.Join(sections, "\n\n")
	if len(text) > 3900 {
		return text[:3850] + "\n…list truncated"
	}
	return text
}

func isDomainIgnored(chatID int64, hostname string) (bool, error) {
	hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
	if configuredDomainMatches(hostname) {
		return true, nil
	}

	domains, err := ignoredDomains(chatID)
	if err != nil {
		return false, err
	}
	for _, domain := range domains {
		if domainMatches(hostname, domain) {
			return true, nil
		}
	}
	return false, nil
}

func domainMatches(hostname, domain string) bool {
	return hostname == domain || strings.HasSuffix(hostname, "."+domain)
}

func getURLs(message *telebot.Message) []string {
	var urls []string

	for _, entity := range message.Entities {
		switch entity.Type {
		case "text_link":
			// Telegram places the destination for formatted links in URL, while
			// the text at Offset/Length is only the label shown to the user.
			if entity.URL != "" {
				urls = append(urls, entity.URL)
			}
		case "url":
			if messageURL, ok := entityText(message.Text, entity.Offset, entity.Length); ok {
				urls = append(urls, messageURL)
			}
		}
	}

	return urls
}

// entityText extracts a Telegram entity, whose offsets are counted in UTF-16
// code units rather than Go runes or bytes.
func entityText(text string, offset, length int) (string, bool) {
	if offset < 0 || length < 0 {
		return "", false
	}

	codeUnits := utf16.Encode([]rune(text))
	end := offset + length
	if end < offset || end > len(codeUnits) {
		return "", false
	}

	return string(utf16.Decode(codeUnits[offset:end])), true
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
