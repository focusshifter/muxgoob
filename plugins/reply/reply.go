package reply

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
)

// RandomGenerator defines an interface for random number generation
type RandomGenerator interface {
	// Intn returns a random int in [0,n)
	Intn(n int) int
}

// RealRandomGenerator implements RandomGenerator using the standard library
type RealRandomGenerator struct {
	rng *rand.Rand
}

func NewRealRandomGenerator() *RealRandomGenerator {
	return &RealRandomGenerator{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (r *RealRandomGenerator) Intn(n int) int {
	return r.rng.Intn(n)
}

// ChatGptClient defines an interface for interacting with ChatGPT
type ChatGptClient interface {
	Ask(message *telebot.Message) string
}

// RealChatGptClient implements ChatGptClient using the actual OpenAI API
type RealChatGptClient struct{}

func (c *RealChatGptClient) Ask(message *telebot.Message) string {
	// The actual implementation will be moved from askChatGpt
	return askChatGpt(message)
}

type ReplyPlugin struct {
	random     RandomGenerator
	chatClient ChatGptClient
}

var (
	sqliteDb        *sql.DB
	techExp         *regexp.Regexp
	questionExp     *regexp.Regexp
	commandExp      *regexp.Regexp
	dotkaExp        *regexp.Regexp
	majorExp        *regexp.Regexp
	replyCmdExp     *regexp.Regexp
	spotifyAlbumExp *regexp.Regexp
)

func init() {
	// Initialize all regexp patterns
	techExp = regexp.MustCompile(`(?i)^\!ттх$`)
	questionExp = regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н)[\s\S]*\?$`)
	commandExp = regexp.MustCompile(`(?i)^(gooby|губи|губ(я)+н),\s*([\s\S]*)$`)
	dotkaExp = regexp.MustCompile(`(?i)^.*(dota|дота|дот((ец)|(к)+(а|у))).*$`)
	majorExp = regexp.MustCompile(`(?i)^.*(товаризч|(товарищ(ь)?)\s+(майор|генерал|старшина|адмирал|капитан)).*$`)
	replyCmdExp = regexp.MustCompile(`^!reply\s+(-?\d+)(?:\s+(.+))?$`)
	spotifyAlbumExp = regexp.MustCompile(`https://open\.spotify\.com/album/([a-zA-Z0-9]+)`)

	// Register with default implementations for production
	plugin := &ReplyPlugin{
		random:     NewRealRandomGenerator(),
		chatClient: &RealChatGptClient{},
	}
	registry.RegisterPlugin(plugin)
}

func (p *ReplyPlugin) Start(_ interface{}) {
	var err error
	sqliteDb, err = sql.Open("sqlite3", "db/muxgoob.sqlite")
	if err != nil {
		log.Fatal("Failed to open SQLite DB:", err)
	}
}

// SetDependencies allows injecting dependencies for testing
func (p *ReplyPlugin) SetDependencies(random RandomGenerator, chatClient ChatGptClient) {
	p.random = random
	p.chatClient = chatClient
}

func (p *ReplyPlugin) Process(message *telebot.Message) {
	// Safety check for nil message
	if message == nil {
		log.Printf("[reply] Received nil message in Process")
		return
	}

	// Safety check for nil bot
	bot := registry.Bot
	if bot == nil {
		log.Printf("[reply] Bot is nil in Process")
		return
	}

	// Safety check for nil message.Chat
	if message.Chat == nil {
		log.Printf("[reply] Message.Chat is nil in Process")
		return
	}

	// Check for !reply command
	if message.Text != "" {
		if matches := replyCmdExp.FindStringSubmatch(message.Text); matches != nil {
			// Only process private messages from the owner
			if message.Chat.Type != telebot.ChatPrivate ||
				message.Sender.Username != registry.Config.OwnerUsername {
				log.Printf("[reply] !reply command received from non-owner: %s", message.Sender.Username)
				return
			}

			chatID, err := strconv.ParseInt(matches[1], 10, 64)
			if err != nil {
				log.Printf("[reply] Invalid chat ID in !reply command: %v", err)
				return
			}

			// Get the message to use for the reply
			var targetMessage *telebot.Message
			if len(matches) > 2 && matches[2] != "" {
				// If message is provided, create a new message with it
				targetMessage = &telebot.Message{
					Chat: &telebot.Chat{ID: chatID},
					Text: matches[2],
				}
			} else {
				// If no message provided, use the last message from the target chat
				history := retrieveHistoryForChat(chatID, 1)
				if len(history) > 0 {
					targetMessage = &history[0]
				} else {
					bot.Send(message.Chat, "No recent messages found in the target chat.")
					return
				}
			}

			// Generate a reply using the chat client
			replyText := p.chatClient.Ask(targetMessage)
			if replyText == "" {
				log.Printf("[reply] No reply generated for chat %d", chatID)
				return
			}

			// Send the reply to the target chat
			var sendOpts *telebot.SendOptions
			if targetMessage.ID != 0 {
				sendOpts = &telebot.SendOptions{ReplyTo: targetMessage}
			}

			_, err = bot.Send(&telebot.Chat{ID: chatID}, replyText, sendOpts)
			if err != nil {
				log.Printf("[reply] Error sending message to chat %d: %v", chatID, err)
				bot.Send(message.Chat, fmt.Sprintf("Error sending message to chat %d: %v", chatID, err))
				return
			}

			// Send a confirmation to the original chat
			bot.Send(message.Chat, fmt.Sprintf("Message sent to chat %d", chatID))
			return
		}
	}

	// Check if this is a reply to bot's message
	if message.ReplyTo != nil {
		log.Printf("[reply] ReplyTo is not nil")
		if message.ReplyTo.Sender != nil {
			log.Printf("[reply] ReplyTo.Sender is not nil, username: %s", message.ReplyTo.Sender.Username)
			// Safety check for bot
			if bot.Me == nil {
				log.Printf("[reply] Bot.Me is nil")
				return
			}

			log.Printf("[reply] Bot.Me is not nil, username: %s", bot.Me.Username)
			if message.ReplyTo.Sender.Username == bot.Me.Username {
				// Use the injected chat client
				log.Printf("[reply] Username matches, sending typing notification")
				bot.Notify(message.Chat, telebot.Typing)
				log.Printf("[reply] Username matches, calling chat client")
				replyText := p.chatClient.Ask(message)
				log.Printf("[reply] Chat client returned: %s", replyText)
				if replyText != "" {
					log.Printf("[reply] Sending reply")
					bot.Send(message.Chat, replyText, &telebot.SendOptions{
						ReplyTo: message})
				}
				return
			} else {
				log.Printf("[reply] Username doesn't match: %s vs %s", message.ReplyTo.Sender.Username, bot.Me.Username)
			}
		} else {
			log.Printf("[reply] ReplyTo.Sender is nil")
		}
	}

	// Regex patterns are now initialized in the init() function

	switch {
	case techExp.MatchString(message.Text):
		bot.Send(message.Chat,
			"ТТХ: "+registry.Config.ReplyTechLink,
			&telebot.SendOptions{DisableWebPagePreview: true, DisableNotification: true})

	case questionExp.MatchString(message.Text):
		bot.Notify(message.Chat, telebot.Typing)
		replyText := p.chatClient.Ask(message)

		if replyText == "" {
			// Use the injected random generator for consistent behavior in tests
			randomValue := p.random.Intn(100)
			switch {
			case randomValue%2 == 0:
				replyText = "Да"
			default:
				replyText = "Нет"
			}
		}

		bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message, ParseMode: telebot.ModeMarkdown})

	case commandExp.MatchString(message.Text):
		bot.Notify(message.Chat, telebot.Typing)
		replyText := p.chatClient.Ask(message)

		if replyText != "" {
			bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message, ParseMode: telebot.ModeMarkdown})
		}

	case dotkaExp.MatchString(message.Text):
		// Use the injected random generator for consistent behavior in tests
		if p.random.Intn(50) == 0 {
			bot.Send(message.Chat, "Щяб в дотку!", &telebot.SendOptions{})
		}

	case majorExp.MatchString(message.Text):
		// Use the injected random generator for consistent behavior in tests
		if p.random.Intn(2) == 0 {
			bot.Send(message.Chat, "Так точно!", &telebot.SendOptions{ReplyTo: message})
		} else {
			bot.Send(message.Chat, "Я за него.", &telebot.SendOptions{ReplyTo: message})
		}

		// case highlightedExp.MatchString(message.Text):
		// 	bot.Send(message.Chat, "herp derp", nil)

	default:
		if p.random.Intn(100) == 0 && len(message.Text) > 150 {
			bot.Notify(message.Chat, telebot.Typing)
			replyText := p.chatClient.Ask(message)

			if replyText != "" {
				bot.Send(message.Chat, replyText, &telebot.SendOptions{ReplyTo: message, ParseMode: telebot.ModeMarkdown})
			}
		}
	}
}

func retrieveHistoryForChat(chatID int64, messageCount int) []telebot.Message {
	// Check if database is initialized
	if sqliteDb == nil {
		log.Printf("[reply] Database not initialized")
		return nil
	}

	rows, err := sqliteDb.Query(
		`SELECT id, reply_to_message_id, data FROM messages 
		WHERE chat_id = ? 
		ORDER BY unixtime DESC LIMIT ?`,
		chatID, messageCount)
	if err != nil {
		log.Printf("Error retrieving chat history: %v", err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	existingIDs := make(map[int]struct{})
	replyParentIDs := make(map[int]struct{})
	for rows.Next() {
		var id int
		var replyID sql.NullInt64
		var data string
		if err := rows.Scan(&id, &replyID, &data); err != nil {
			log.Printf("[reply] Error scanning message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("[reply] Error unmarshaling message: %v", err)
			continue
		}
		existingIDs[id] = struct{}{}
		if replyID.Valid {
			replyParentIDs[int(replyID.Int64)] = struct{}{}
		}
		messages = append(messages, msg)
	}

	for id := range existingIDs {
		delete(replyParentIDs, id)
	}

	if len(replyParentIDs) > 0 {
		parentMessages := retrieveMessagesByIDs(sqliteDb, chatID, replyParentIDs)
		messages = append(messages, parentMessages...)
	}

	// Sort by ID for consistent order
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Unixtime < messages[j].Unixtime
	})

	log.Printf("[reply] Retrieved %v messages", len(messages))

	return messages
}

func retrieveMessagesByIDs(db *sql.DB, chatID int64, idSet map[int]struct{}) []telebot.Message {
	if len(idSet) == 0 {
		return nil
	}

	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	query := fmt.Sprintf(
		`SELECT data FROM messages WHERE chat_id = ? AND id IN (%s)`,
		placeholders,
	)

	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, chatID)
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		log.Printf("[reply] Error retrieving parent messages: %v", err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			log.Printf("[reply] Error scanning parent message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("[reply] Error unmarshaling parent message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages
}

func retrieveChatMembers(chatID int64, maxMembers int) []string {
	if sqliteDb == nil {
		log.Printf("[reply] Database not initialized when retrieving members")
		return nil
	}

	var count int
	err := sqliteDb.QueryRow(
		`SELECT COUNT(DISTINCT sender_id) FROM messages WHERE chat_id = ?`,
		chatID,
	).Scan(&count)
	if err != nil {
		log.Printf("[reply] Error counting chat members: %v", err)
		return nil
	}
	if count == 0 {
		return nil
	}

	query := `
		SELECT u.username, u.first_name, u.last_name, m.sender_id
		FROM messages m
		JOIN users u ON u.id = m.sender_id
		WHERE m.chat_id = ?
		GROUP BY m.sender_id
		ORDER BY MAX(m.unixtime) DESC`
	var rows *sql.Rows
	if count > maxMembers {
		query += " LIMIT ?"
		rows, err = sqliteDb.Query(query, chatID, maxMembers)
	} else {
		rows, err = sqliteDb.Query(query, chatID)
	}
	if err != nil {
		log.Printf("[reply] Error retrieving chat members: %v", err)
		return nil
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var username, firstName, lastName sql.NullString
		var senderID int64
		if err := rows.Scan(&username, &firstName, &lastName, &senderID); err != nil {
			log.Printf("[reply] Error scanning chat member: %v", err)
			continue
		}
		name := strings.TrimSpace(strings.Join([]string{firstName.String, lastName.String}, " "))
		if username.Valid && username.String != "" {
			members = append(members, username.String)
		} else if name != "" {
			members = append(members, name)
		} else {
			members = append(members, fmt.Sprintf("user_%d", senderID))
		}
	}

	if err := rows.Err(); err != nil {
		log.Printf("[reply] Error iterating chat members: %v", err)
		return nil
	}

	return members
}

func generateChatGptHistory(messages []telebot.Message) string {
	var history string
	var username string

	for _, message := range messages {
		if message.Sender.Username != "" {
			username = message.Sender.Username
		} else {
			username = message.Sender.FirstName + " " + message.Sender.LastName
		}
		history += fmt.Sprintf("%s: %s\n", username, message.Text)
	}

	return history
}

func buildNoAssPrefill(messages []telebot.Message, questionText string, systemPrompt string, botID int, currentMessage *telebot.Message, members []string) string {
	var prefill strings.Builder

	if strings.TrimSpace(systemPrompt) != "" {
		prefill.WriteString(systemPrompt)
		prefill.WriteString("\n\n")
	}

	if len(members) > 0 {
		prefill.WriteString("Chat members: ")
		prefill.WriteString(strings.Join(members, ", "))
		prefill.WriteString("\n\n")
	}

	if spotifyContext := buildSpotifyReviewContext(messages); spotifyContext != "" {
		prefill.WriteString("Mentioned spotify albums:\n")
		prefill.WriteString(spotifyContext)
		prefill.WriteString("\n\n")
	}

	prefill.WriteString("Prefill:\n")

	for _, message := range messages {
		if currentMessage != nil && message.ID == currentMessage.ID {
			continue
		}
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		name := message.Sender.Username
		if name == "" {
			name = strings.TrimSpace(message.Sender.FirstName + " " + message.Sender.LastName)
		}
		role := "{{user}}"
		if botID != 0 && message.Sender.ID == botID {
			role = "{{char}}"
		}
		prefill.WriteString(fmt.Sprintf("%s (%s): %s\n", role, name, message.Text))
	}

	currentName := ""
	if currentMessage != nil && currentMessage.Sender != nil {
		currentName = currentMessage.Sender.Username
		if currentName == "" {
			currentName = strings.TrimSpace(currentMessage.Sender.FirstName + " " + currentMessage.Sender.LastName)
		}
	}
	if currentName != "" {
		prefill.WriteString(fmt.Sprintf("{{user}} (%s): %s\n", currentName, questionText))
	} else {
		prefill.WriteString(fmt.Sprintf("{{user}}: %s\n", questionText))
	}
	return prefill.String()
}

func buildSpotifyReviewContext(messages []telebot.Message) string {
	if sqliteDb == nil {
		return ""
	}

	albumIDs := extractSpotifyAlbumIDs(messages)
	if len(albumIDs) == 0 {
		return ""
	}

	reviewTexts := lookupSpotifyReviewTexts(albumIDs)
	if len(reviewTexts) == 0 {
		return ""
	}

	var out strings.Builder
	for _, albumID := range albumIDs {
		reviewText, ok := reviewTexts[albumID]
		if !ok {
			continue
		}
		reviewText = strings.TrimSpace(reviewText)
		if reviewText == "" {
			continue
		}
		out.WriteString(fmt.Sprintf("Album %s review: %s\n", albumID, reviewText))
	}

	return strings.TrimSpace(out.String())
}

var fetchTelegraphReviewText = realFetchTelegraphReviewText

func extractSpotifyAlbumIDs(messages []telebot.Message) []string {
	seen := make(map[string]struct{})
	var ordered []string
	for _, message := range messages {
		text := strings.TrimSpace(message.Text)
		if message.Caption != "" {
			text = strings.TrimSpace(text + " " + message.Caption)
		}
		if text == "" {
			continue
		}
		matches := spotifyAlbumExp.FindAllStringSubmatch(text, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			albumID := match[1]
			if _, ok := seen[albumID]; ok {
				continue
			}
			seen[albumID] = struct{}{}
			ordered = append(ordered, albumID)
		}
	}
	return ordered
}

func lookupSpotifyReviewTexts(albumIDs []string) map[string]string {
	if len(albumIDs) == 0 {
		return nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(albumIDs)), ",")
	query := fmt.Sprintf(
		`SELECT item_key, review_url, review_text FROM spotify_reviews
		WHERE type = 'album' AND item_key IN (%s)`,
		placeholders,
	)

	args := make([]interface{}, 0, len(albumIDs))
	for _, id := range albumIDs {
		args = append(args, id)
	}

	rows, err := sqliteDb.Query(query, args...)
	if err != nil {
		log.Printf("[reply] Error retrieving spotify reviews: %v", err)
		return nil
	}

	reviewTexts := make(map[string]string)
	pendingUpdates := make(map[string]string)
	for rows.Next() {
		var itemKey, reviewURL, reviewText string
		if err := rows.Scan(&itemKey, &reviewURL, &reviewText); err != nil {
			log.Printf("[reply] Error scanning spotify review: %v", err)
			continue
		}
		reviewText = strings.TrimSpace(reviewText)
		if reviewText == "" && strings.TrimSpace(reviewURL) != "" {
			fetched := strings.TrimSpace(fetchTelegraphReviewText(reviewURL))
			if fetched != "" {
				reviewText = fetched
				pendingUpdates[itemKey] = fetched
			}
		}
		if reviewText != "" {
			reviewTexts[itemKey] = reviewText
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[reply] Error iterating spotify reviews: %v", err)
	}
	if err := rows.Close(); err != nil {
		log.Printf("[reply] Error closing spotify review rows: %v", err)
	}
	for itemKey, reviewText := range pendingUpdates {
		if err := saveSpotifyReviewText(itemKey, reviewText); err != nil {
			log.Printf("[reply] Error saving spotify review text: %v", err)
		}
	}
	return reviewTexts
}

func saveSpotifyReviewText(itemKey, reviewText string) error {
	if sqliteDb == nil || strings.TrimSpace(reviewText) == "" {
		return nil
	}
	_, err := sqliteDb.Exec(
		"UPDATE spotify_reviews SET review_text = ? WHERE type = 'album' AND item_key = ?",
		reviewText, itemKey,
	)
	return err
}

func realFetchTelegraphReviewText(reviewURL string) string {
	parsed, err := url.Parse(reviewURL)
	if err != nil {
		return ""
	}
	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		return ""
	}
	apiURL := fmt.Sprintf("https://api.telegra.ph/getPage/%s?return_content=true", url.PathEscape(path))
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return ""
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Content []interface{} `json:"content"`
		} `json:"result"`
		Error string `json:"error"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&result); err != nil {
		return ""
	}
	if !result.OK {
		return ""
	}
	text := strings.TrimSpace(extractTelegraphText(result.Result.Content))
	if text == "" {
		return ""
	}
	return truncateText(text, 1200)
}

func extractTelegraphText(node interface{}) string {
	switch v := node.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			text := strings.TrimSpace(extractTelegraphText(item))
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]interface{}:
		if children, ok := v["children"]; ok {
			return extractTelegraphText(children)
		}
	}
	return ""
}

func truncateText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "…"
}

// askChatGpt is a variable function that can be replaced in tests
var askChatGpt = func(message *telebot.Message) string {
	// Safety check for test environment
	if message == nil {
		log.Printf("[reply] Message is nil in askChatGpt")
		return ""
	}

	question := message.Text

	// No need to check if registry.Config is initialized as it's not a pointer type

	var config openai.ClientConfig
	var model string

	// Get chat ID for chat-specific settings
	var chatID *int64
	if message.Chat != nil {
		chatID = &message.Chat.ID
	}

	// Get AI provider from database with fallback to config.yml
	aiProvider := registry.GetAiProvider(chatID)

	if aiProvider == "openrouter" {
		config = openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		// Get AI model from database with fallback to config.yml
		model = registry.GetAiModel(chatID)
	} else {
		config = openai.DefaultConfig(registry.Config.OpenaiApiKey)
		model = "gpt-4o-mini"
	}

	client := openai.NewClientWithConfig(config)

	// Get prompt from promptmgr
	// Check if message.Chat is nil to prevent nil pointer dereference
	if message.Chat == nil {
		log.Printf("[reply] Message.Chat is nil in askChatGpt")
		return ""
	}

	systemMessage, err := promptmgr.GetCurrentPrompt(message.Chat.ID, true)
	if err != nil {
		log.Printf("[reply] Error getting prompt: %v", err)
		return ""
	}

	userMessage := fmt.Sprintf(registry.Config.ChatGptUserPrompt, question)

	log.Printf("ChatGPT request: model %v", model)
	log.Printf("ChatGPT request: chat_id %v", message.Chat.ID)
	log.Printf("ChatGPT request: system %v", systemMessage)
	log.Printf("ChatGPT request: user %v", userMessage)

	botID := 0
	if registry.Bot != nil && registry.Bot.Bot != nil {
		botID = registry.Bot.Bot.Me.ID
	}

	var members []string
	if message.Chat != nil {
		members = retrieveChatMembers(message.Chat.ID, 50)
	}

	if registry.Config.ChatGptUseHistory {
		// Check if message.Chat is nil to prevent nil pointer dereference
		if message.Chat == nil {
			log.Printf("[reply] Message.Chat is nil when retrieving history")
			userMessage = buildNoAssPrefill(nil, userMessage, systemMessage, botID, message, members)
		} else {
			historyMessages := retrieveHistoryForChat(message.Chat.ID, registry.Config.ChatGptHistoryDepth)
			userMessage = buildNoAssPrefill(historyMessages, userMessage, systemMessage, botID, message, members)

			log.Printf("[reply] ChatGPT request: noass prefill %v", userMessage)
		}
	} else {
		userMessage = buildNoAssPrefill(nil, question, systemMessage, botID, message, members)
	}

	resp, err := client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:            model,
			Temperature:      0.3,
			TopP:             1.0,
			FrequencyPenalty: 0.2,
			PresencePenalty:  0.1,

			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userMessage,
				},
			},
		},
	)

	if err != nil {
		log.Printf("ChatCompletion error: %v", err)
		return ""
	}

	return resp.Choices[0].Message.Content
}
