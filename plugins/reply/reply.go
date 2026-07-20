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

	"github.com/focusshifter/muxgoob/internal/openaicodex"
	chattools "github.com/focusshifter/muxgoob/internal/tools"
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

const actionOnlyReplyToken = "__GOOBY_ACTION_ONLY__"

func (c *RealChatGptClient) Ask(message *telebot.Message) string {
	// The actual implementation will be moved from askChatGpt
	return askChatGpt(message)
}

func appendImageGenerationToolIfEnabled(chatID int64, tools []chattools.Tool, toolSystemParts []string) ([]chattools.Tool, []string, *chattools.GenerateImageTool) {
	if !registry.GetImageGenerationEnabled(chatID) {
		return tools, toolSystemParts, nil
	}
	imageTool := chattools.NewGenerateImageTool(chatID)
	tools = append(tools, imageTool)
	toolSystemParts = append(toolSystemParts,
		"If the user asks you to draw, generate, create, render, or make an image/photo/picture/sticker/картинку/мем, use generateImage instead of only describing an image.",
		"For a new image request, use only the active request as visual source unless the user explicitly asks for chat history, chat events, or chat participants. In that opt-in case, use only relevant factual text context; never carry over prior image prompts, generated images, captions, styles, or image metadata. Preserve all explicit visual constraints, especially composition, lighting, color grade, era, and negative constraints.",
		"When using generateImage, never expose the internal image prompt. If a caption feels useful, pass a short related Telegram-style caption to the tool; otherwise leave the caption empty.",
		"After generateImage succeeds, do not send any follow-up confirmation text.",
	)
	return tools, toolSystemParts, imageTool
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
	mentionExp      *regexp.Regexp
)

func init() {
	// Initialize all regexp patterns
	techExp = regexp.MustCompile(`(?i)^\!ттх$`)
	questionExp = regexp.MustCompile(`(?i)^.*(gooby|губи|губ(я)+н)[\s\S]*\?$`)
	commandExp = regexp.MustCompile(`(?i)^(gooby|губи|губ(я)+н)(?:,\s*|\s+)([\s\S]*)$`)
	dotkaExp = regexp.MustCompile(`(?i)^.*(dota|дота|дот((ец)|(к)+(а|у))).*$`)
	majorExp = regexp.MustCompile(`(?i)^.*(товаризч|(товарищ(ь)?)\s+(майор|генерал|старшина|адмирал|капитан)).*$`)
	replyCmdExp = regexp.MustCompile(`^!reply\s+(-?\d+)(?:\s+(.+))?$`)
	spotifyAlbumExp = regexp.MustCompile(`https://open\.spotify\.com/album/([a-zA-Z0-9]+)`)
	mentionExp = regexp.MustCompile(`(?i)@([a-z0-9_]{3,})`)

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

	maybeQueueImageMetadata(message)

	// Check for !reply command
	messageText := messagePromptText(message)
	if messageText != "" {
		if matches := replyCmdExp.FindStringSubmatch(messageText); matches != nil {
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
				if replyText != "" && !isActionOnlyReply(replyText) {
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
	case techExp.MatchString(messageText):
		bot.Send(message.Chat,
			"ТТХ: "+registry.Config.ReplyTechLink,
			&telebot.SendOptions{DisableWebPagePreview: true, DisableNotification: true})

	case questionExp.MatchString(messageText):
		bot.Notify(message.Chat, telebot.Typing)
		replyText := p.chatClient.Ask(message)
		if isActionOnlyReply(replyText) {
			return
		}

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

		sendReplyWithLog(bot, message.Chat, replyText, replyOptionsForMessage(message))

	case commandExp.MatchString(messageText):
		bot.Notify(message.Chat, telebot.Typing)
		replyText := p.chatClient.Ask(message)

		if replyText != "" && !isActionOnlyReply(replyText) {
			sendReplyWithLog(bot, message.Chat, replyText, replyOptionsForMessage(message))
		}

	case dotkaExp.MatchString(messageText):
		// Use the injected random generator for consistent behavior in tests
		if p.random.Intn(50) == 0 {
			bot.Send(message.Chat, "Щяб в дотку!", &telebot.SendOptions{})
		}

	case majorExp.MatchString(messageText):
		// Use the injected random generator for consistent behavior in tests
		if p.random.Intn(2) == 0 {
			bot.Send(message.Chat, "Так точно!", &telebot.SendOptions{ReplyTo: message})
		} else {
			bot.Send(message.Chat, "Я за него.", &telebot.SendOptions{ReplyTo: message})
		}

		// case highlightedExp.MatchString(message.Text):
		// 	bot.Send(message.Chat, "herp derp", nil)

	default:
		if p.random.Intn(100) == 0 && len(messageText) > 150 {
			bot.Notify(message.Chat, telebot.Typing)
			replyText := p.chatClient.Ask(message)

			if replyText != "" && !isActionOnlyReply(replyText) {
				sendReplyWithLog(bot, message.Chat, replyText, &telebot.SendOptions{ReplyTo: message})
			}
		}
	}
}

func isActionOnlyReply(replyText string) bool {
	return replyText == actionOnlyReplyToken
}

func messagePromptText(message *telebot.Message) string {
	if message == nil {
		return ""
	}
	text := strings.TrimSpace(message.Text)
	if text != "" {
		return text
	}
	return strings.TrimSpace(message.Caption)
}

var directImageRequestPattern = regexp.MustCompile(`(?i)(^|[\s,.:;!?])(нарисуй|рисуй|сгенерируй|создай|сделай\s+(?:картинк|изображени|мем)|draw|generate|create|render)(?:$|[\s,.:;!?])`)

func shouldIsolateImageGenerationPrompt(question string) bool {
	return directImageRequestPattern.MatchString(strings.TrimSpace(question))
}

var imageSceneContextPattern = regexp.MustCompile(`(?i)(на\s+основании\s+(?:истории|чата)|опираясь\s+на\s+(?:истори|событи|чат)|по\s+(?:истории|событиям)\s+чата|с\s+участниками\s+чата|based\s+on\s+(?:the\s+)?(?:chat|conversation|history)|using\s+(?:the\s+)?chat\s+history|with\s+(?:the\s+)?chat\s+participants)`)

func shouldUseImageSceneContext(question string) bool {
	return imageSceneContextPattern.MatchString(strings.TrimSpace(question))
}

var retrospectiveQuestionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(обсуждал[аио]?|вспомни|в истории|раньше|когда[- ]?то|кто говорил|найди|искал[аи]?|что мы говорили|what did we|did we|who said|find messages|search the chat)`),
	regexp.MustCompile(`(?i)(сообщени|чат|истори|discussion|history|messages?).*(про|about)`),
}

var historyBoundsQuestionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(перв(ое|ый|ая)?|сам(ое|ый|ая)?\s+ранн|стар(ое|ый|ая)?\s+сообщени|раньше\s+всего|earliest|oldest|first\s+message|history\s+start)`),
}

func shouldForceHistoryBounds(question string) bool {
	question = strings.TrimSpace(question)
	if question == "" {
		return false
	}
	for _, pattern := range historyBoundsQuestionPatterns {
		if pattern.MatchString(question) {
			return true
		}
	}
	return false
}

func shouldForceSearchMessages(question string) bool {
	question = strings.TrimSpace(question)
	if question == "" {
		return false
	}
	for _, pattern := range retrospectiveQuestionPatterns {
		if pattern.MatchString(question) {
			return true
		}
	}
	return false
}

func initialToolChoice(forceSearch, forceHistoryBounds bool) any {
	if forceHistoryBounds {
		return openai.ToolChoice{
			Type:     openai.ToolTypeFunction,
			Function: openai.ToolFunction{Name: "getChatHistoryBounds"},
		}
	}
	if forceSearch {
		return openai.ToolChoice{
			Type:     openai.ToolTypeFunction,
			Function: openai.ToolFunction{Name: "searchMessages"},
		}
	}
	return nil
}

func formatChatGPTRequestLog(provider string, model string, chatID int64, questionLen int, toolCount int) string {
	return fmt.Sprintf("[reply] ChatGPT request provider=%s model=%s chat_id=%d question_len=%d tools=%d", provider, model, chatID, questionLen, toolCount)
}

func replyOptionsForMessage(message *telebot.Message) *telebot.SendOptions {
	if message == nil || message.ID == 0 {
		return nil
	}
	return &telebot.SendOptions{ReplyTo: message}
}

func sendReplyWithLog(bot *registry.BotWrapper, chat *telebot.Chat, text string, opts *telebot.SendOptions) {
	if bot == nil || chat == nil {
		log.Printf("[reply] Cannot send reply: bot or chat is nil")
		return
	}

	_, err := bot.Send(chat, text, opts)
	if err == nil {
		log.Printf("[reply] Sent reply len=%d", len(text))
		return
	}

	preview := strings.TrimSpace(text)
	if len(preview) > 160 {
		preview = preview[:160] + "..."
	}
	parseMode := telebot.ParseMode("")
	if opts != nil {
		parseMode = opts.ParseMode
	}
	log.Printf("[reply] Error sending reply parse_mode=%q len=%d preview=%q err=%v", parseMode, len(text), preview, err)

	if opts != nil && opts.ParseMode != "" {
		fallbackOpts := *opts
		fallbackOpts.ParseMode = ""
		if _, fallbackErr := bot.Send(chat, text, &fallbackOpts); fallbackErr != nil {
			log.Printf("[reply] Fallback send without parse mode failed: %v", fallbackErr)
		} else {
			log.Printf("[reply] Fallback send without parse mode succeeded len=%d", len(text))
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
		if message.Sender == nil {
			continue
		}
		if message.Sender.Username != "" {
			username = message.Sender.Username
		} else {
			username = strings.TrimSpace(message.Sender.FirstName + " " + message.Sender.LastName)
			if username == "" {
				username = fmt.Sprintf("user_%d", message.Sender.ID)
			}
		}
		history += fmt.Sprintf("%s: %s\n", username, message.Text)
	}

	return history
}

func userDisplayName(user *telebot.User) string {
	if user == nil {
		return ""
	}
	if user.Username != "" {
		return user.Username
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name != "" {
		return name
	}
	if user.ID != 0 {
		return fmt.Sprintf("user_%d", user.ID)
	}
	return ""
}

func buildPersonFactsContext(chatID int64, messages []telebot.Message, currentMessage *telebot.Message, botID int) string {
	orderedUserIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	userNames := make(map[int64]string)

	addUser := func(user *telebot.User) {
		if user == nil || user.ID == 0 || user.ID == botID {
			return
		}
		userID := int64(user.ID)
		if _, ok := seen[userID]; !ok {
			seen[userID] = struct{}{}
			orderedUserIDs = append(orderedUserIDs, userID)
		}
		if name := userDisplayName(user); name != "" {
			userNames[userID] = name
		}
	}

	for _, message := range messages {
		addUser(message.Sender)
	}
	if currentMessage != nil {
		addUser(currentMessage.Sender)
		addMentionedUsers(chatID, currentMessage, addUser)
	}

	if len(orderedUserIDs) == 0 {
		return ""
	}

	factMap, err := promptmgr.GetPersonFactsMulti(chatID, orderedUserIDs)
	if err != nil {
		log.Printf("[reply] Error retrieving person facts: %v", err)
		return ""
	}

	var out strings.Builder
	for _, userID := range orderedUserIDs {
		facts := strings.TrimSpace(factMap[userID])
		if facts == "" {
			continue
		}
		name := userNames[userID]
		if name == "" {
			name = fmt.Sprintf("user_%d", userID)
		}
		out.WriteString(name)
		out.WriteString(": ")
		out.WriteString(facts)
		out.WriteString("\n")
	}

	return strings.TrimSpace(out.String())
}

func addMentionedUsers(chatID int64, message *telebot.Message, addUser func(user *telebot.User)) {
	if message == nil {
		return
	}
	for _, entity := range message.Entities {
		if entity.Type == telebot.EntityTMention && entity.User != nil {
			addUser(entity.User)
		}
	}
	if sqliteDb == nil || strings.TrimSpace(message.Text) == "" {
		return
	}
	seen := make(map[string]struct{})
	for _, match := range mentionExp.FindAllStringSubmatch(message.Text, -1) {
		if len(match) < 2 {
			continue
		}
		username := strings.ToLower(strings.TrimSpace(match[1]))
		if username == "" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		user := lookupChatUserByUsername(chatID, username)
		if user != nil {
			addUser(user)
		}
	}
}

func hasExplicitMention(message *telebot.Message) bool {
	if message == nil {
		return false
	}
	for _, entity := range message.Entities {
		if entity.Type == telebot.EntityTMention && entity.User != nil {
			return true
		}
	}
	return mentionExp.MatchString(message.Text)
}

func lookupChatUserByUsername(chatID int64, username string) *telebot.User {
	if sqliteDb == nil || strings.TrimSpace(username) == "" {
		return nil
	}
	row := sqliteDb.QueryRow(`
		SELECT u.id, COALESCE(u.username, ''), COALESCE(u.first_name, ''), COALESCE(u.last_name, '')
		FROM users u
		WHERE LOWER(u.username) = LOWER(?)
		AND EXISTS (
			SELECT 1 FROM messages m WHERE m.chat_id = ? AND m.sender_id = u.id LIMIT 1
		)
		LIMIT 1`, username, chatID)
	var id int
	var resolvedUsername, firstName, lastName string
	if err := row.Scan(&id, &resolvedUsername, &firstName, &lastName); err != nil {
		return nil
	}
	return &telebot.User{ID: id, Username: resolvedUsername, FirstName: firstName, LastName: lastName}
}

const maxImageSceneContextMessages = 12

func imageSceneRelevantMessages(messages []telebot.Message, botID int, currentMessage *telebot.Message) []telebot.Message {
	relevant := make([]telebot.Message, 0, maxImageSceneContextMessages)
	for _, message := range messages {
		if currentMessage != nil && message.ID == currentMessage.ID {
			continue
		}
		if message.Sender != nil && botID != 0 && message.Sender.ID == botID {
			continue
		}
		if message.Photo != nil {
			continue
		}
		text := messagePromptText(&message)
		if text == "" || shouldIsolateImageGenerationPrompt(text) {
			continue
		}
		relevant = append(relevant, message)
	}
	if len(relevant) > maxImageSceneContextMessages {
		relevant = relevant[len(relevant)-maxImageSceneContextMessages:]
	}
	return relevant
}

func buildImageScenePrompt(messages []telebot.Message, question string, botID int, currentMessage *telebot.Message, members []string, personFacts string) string {
	contextLines := make([]string, 0, maxImageSceneContextMessages)
	for _, message := range imageSceneRelevantMessages(messages, botID, currentMessage) {
		text := messagePromptText(&message)
		name := "participant"
		if message.Sender != nil {
			name = message.Sender.Username
			if name == "" {
				name = strings.TrimSpace(message.Sender.FirstName + " " + message.Sender.LastName)
			}
		}
		contextLines = append(contextLines, fmt.Sprintf("%s: %s", name, text))
	}

	var prompt strings.Builder
	prompt.WriteString("Current image request (authoritative):\n")
	prompt.WriteString(strings.TrimSpace(question))
	prompt.WriteString("\n\nThe user explicitly requested scene context from this chat. Use only factual details that help this specific scene; ignore unrelated discussion and every prior image prompt, caption, image, style, or color grade.\n")
	if len(members) > 0 {
		prompt.WriteString("Chat participants (use only if the request asks for participants): ")
		prompt.WriteString(strings.Join(members, ", "))
		prompt.WriteString("\n")
	}
	if strings.TrimSpace(personFacts) != "" {
		prompt.WriteString("Relevant participant facts (use only if they help this scene; do not turn them into visual instructions unless requested):\n")
		prompt.WriteString(strings.TrimSpace(personFacts))
		prompt.WriteString("\n")
	}
	if len(contextLines) > 0 {
		prompt.WriteString("Recent text-only chat context:\n")
		prompt.WriteString(strings.Join(contextLines, "\n"))
	}
	return prompt.String()
}

func buildImageMentionPrompt(question, personFacts string) string {
	if strings.TrimSpace(personFacts) == "" {
		return question
	}
	return strings.TrimSpace(question) + "\n\nExplicitly mentioned chat-member facts (use only when relevant to the requested scene; do not invent visual traits from them):\n" + strings.TrimSpace(personFacts)
}

func buildNoAssPrefill(messages []telebot.Message, questionText string, systemPrompt string, personFacts string, botID int, currentMessage *telebot.Message, members []string) string {
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

	if strings.TrimSpace(personFacts) != "" {
		prefill.WriteString("Chat member profiles:\n")
		prefill.WriteString(strings.TrimSpace(personFacts))
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
		messageText := strings.TrimSpace(message.Text)
		if messageText == "" {
			messageText = strings.TrimSpace(message.Caption)
		}
		if metadata := strings.TrimSpace(imageMetadataForMessage(message)); metadata != "" {
			if messageText != "" {
				messageText += "\n"
			}
			messageText += "Image metadata: " + metadata
		}
		if messageText == "" {
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
		prefill.WriteString(fmt.Sprintf("%s (%s): %s\n", role, name, messageText))
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

func fetchPrefillMembers(chatID int64) []string {
	tool := chattools.NewFetchUsersTool(sqliteDb, chatID)
	result, err := tool.Execute(context.Background(), `{"limit":50}`)
	if err != nil {
		log.Printf("[reply] Error fetching prefill members: %v", err)
		return nil
	}

	var payload struct {
		Users []string `json:"users"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		log.Printf("[reply] Error decoding prefill members: %v", err)
		return nil
	}

	return payload.Users
}

// askChatGpt is a variable function that can be replaced in tests
var askChatGpt = func(message *telebot.Message) string {
	// Safety check for test environment
	if message == nil {
		log.Printf("[reply] Message is nil in askChatGpt")
		return ""
	}

	question := messagePromptText(message)
	if imageContext, imageFallback, handled := maybeBuildImageInspectionContext(message, question); handled {
		if imageFallback != "" {
			return imageFallback
		}
		question = strings.TrimSpace(strings.Join([]string{question, "", imageContext}, "\n"))
	}

	// No need to check if registry.Config is initialized as it's not a pointer type

	var client chattools.ChatCompletionCreator
	var model string

	// Get chat ID for chat-specific settings
	var chatID *int64
	if message.Chat != nil {
		chatID = &message.Chat.ID
	}

	// Get AI provider from database with fallback to config.yml
	aiProvider := registry.GetAiProvider(chatID)
	effectiveProvider := aiProvider

	switch aiProvider {
	case "openrouter":
		config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		model = registry.GetAiModel(chatID)
		client = openai.NewClientWithConfig(config)
	case "openai-codex":
		configuredModel := strings.TrimSpace(registry.GetAiModel(chatID))
		if configuredModel == "" {
			configuredModel = "gpt-5.4"
		}
		modelInfo := openaicodex.NormalizeConfiguredModel(configuredModel)
		if modelInfo.UseCodex {
			fallbackConfig := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
			fallbackConfig.BaseURL = "https://openrouter.ai/api/v1"
			client = openaicodex.NewClient(openaicodex.WithFallbackClient(openai.NewClientWithConfig(fallbackConfig)))
			model = configuredModel
			effectiveProvider = "openai-codex"
		} else {
			config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
			config.BaseURL = "https://openrouter.ai/api/v1"
			client = openai.NewClientWithConfig(config)
			model = modelInfo.OpenRouterModel
			effectiveProvider = "openrouter"
		}
	default:
		config := openai.DefaultConfig(registry.Config.OpenaiApiKey)
		model = "gpt-4o-mini"
		client = openai.NewClientWithConfig(config)
	}

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

	pollTool := chattools.NewSendPollTool(message.Chat.ID)
	var imageTool *chattools.GenerateImageTool
	tools := []chattools.Tool{
		chattools.NewFetchUsersTool(sqliteDb, message.Chat.ID),
		chattools.NewChatHistoryBoundsTool(sqliteDb, message.Chat.ID),
		chattools.NewSearchMessagesTool(sqliteDb, message.Chat.ID, message.ID),
		chattools.NewGetUserFactsTool(sqliteDb, message.Chat.ID),
		chattools.NewRememberTopicTool(sqliteDb, message.Chat.ID),
		chattools.NewForgetTopicTool(sqliteDb, message.Chat.ID),
		pollTool,
	}
	toolSystemParts := []string{
		"You can call tools when they are needed.",
		"Avoid markdown. If formatting is needed, use Telegram markdown only.",
		"If the user asks you to create or post a poll/opros, use sendPoll instead of writing plain-text checkbox options.",
		"After sendPoll succeeds, do not send any follow-up confirmation text.",
	}
	if registry.GetImageGenerationEnabled(message.Chat.ID) {
		tools, toolSystemParts, imageTool = appendImageGenerationToolIfEnabled(message.Chat.ID, tools, toolSystemParts)
	}
	forceSearch := shouldForceSearchMessages(question)
	forceHistoryBounds := shouldForceHistoryBounds(question)
	toolRegistry := chattools.NewRegistry(tools...)
	toolSystemParts = append(toolSystemParts,
		"Use fetchUsers for questions about who is in the chat, chat participants, usernames, or active members.",
		"Use getUserFacts for questions about specific users, what is known about them, or when you need facts for one or more people in this chat.",
		"If the user asks what you know about a person or mentions a specific @username or name, prefer getUserFacts to verify chat-scoped facts, especially if the person is unfamiliar or not clearly covered by the prefill.",
		"Use rememberTopic when the user directly asks you to remember or keep some durable chat lore, topic, preference, or instruction for later.",
		"Use forgetTopic when the user directly asks you to forget, remove, or stop remembering a durable chat topic or lore item.",
		"When asked about a person, do not dump every stored fact. Pick no more than 3 of the most interesting, relevant, or distinctive facts and summarize them.",
		"Avoid meta commentary about hidden context, missing prompt data, or refusing to speculate. Just answer briefly with the best supported facts you have.",
		"Use getChatHistoryBounds for questions asking for the first, oldest, earliest, latest, or total stored chat history; do not infer chronological bounds from a topic search.",
		"Use searchMessages for questions that require looking up prior messages instead of guessing from the prefill. For questions about when a topic was first discussed or mentioned, call searchMessages with sort set to oldest.",
		"For questions about prior discussions, whether something was discussed before, who said something, finding old messages, or what someone thinks based on chat history, you must call searchMessages before answering. For chronological bounds, call getChatHistoryBounds instead.",
		"Treat the prefill as recent context only, not authoritative chat history.",
		"When using searchMessages for a topic, generate full-word variants yourself when useful, including transliterations, spacing variants, alternate spellings, abbreviations, and closely related names.",
	)
	toolSystemMessage := strings.Join(toolSystemParts, " ")

	userMessage := fmt.Sprintf(registry.Config.ChatGptUserPrompt, question)

	log.Print(formatChatGPTRequestLog(effectiveProvider, model, message.Chat.ID, len(question), len(toolRegistry.Definitions())))

	botID := 0
	if registry.Bot != nil && registry.Bot.Bot != nil {
		botID = registry.Bot.Bot.Me.ID
	}

	members := fetchPrefillMembers(message.Chat.ID)

	if shouldIsolateImageGenerationPrompt(question) {
		if shouldUseImageSceneContext(question) && message.Chat != nil {
			imageHistory := retrieveHistoryForChat(message.Chat.ID, registry.Config.ChatGptHistoryDepth)
			relevantSceneMessages := imageSceneRelevantMessages(imageHistory, botID, message)
			personFacts := buildPersonFactsContext(message.Chat.ID, relevantSceneMessages, message, botID)
			userMessage = buildImageScenePrompt(relevantSceneMessages, question, botID, message, members, personFacts)
		} else if message.Chat != nil && hasExplicitMention(message) {
			personFacts := buildPersonFactsContext(message.Chat.ID, nil, message, botID)
			userMessage = buildImageMentionPrompt(question, personFacts)
		} else {
			// A new image request must not inherit prior image prompts, captions, or chat lore.
			userMessage = fmt.Sprintf(registry.Config.ChatGptUserPrompt, question)
		}
	} else if registry.Config.ChatGptUseHistory {
		// Check if message.Chat is nil to prevent nil pointer dereference
		if message.Chat == nil {
			log.Printf("[reply] Message.Chat is nil when retrieving history")
			userMessage = buildNoAssPrefill(nil, userMessage, systemMessage, "", botID, message, members)
		} else {
			historyMessages := retrieveHistoryForChat(message.Chat.ID, registry.Config.ChatGptHistoryDepth)
			personFacts := buildPersonFactsContext(message.Chat.ID, historyMessages, message, botID)
			userMessage = buildNoAssPrefill(historyMessages, userMessage, systemMessage, personFacts, botID, message, members)

		}
	} else {
		personFacts := buildPersonFactsContext(message.Chat.ID, nil, message, botID)
		userMessage = buildNoAssPrefill(nil, question, systemMessage, personFacts, botID, message, members)
	}

	resp, err := chattools.RunLoop(
		context.Background(),
		client,
		openai.ChatCompletionRequest{
			Model:            model,
			Temperature:      0.3,
			TopP:             1.0,
			FrequencyPenalty: 0.2,
			PresencePenalty:  0.1,
			Tools:            toolRegistry.Definitions(),
			ToolChoice:       initialToolChoice(forceSearch, forceHistoryBounds),
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: toolSystemMessage,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userMessage,
				},
			},
		},
		toolRegistry,
		5,
	)

	if err != nil {
		log.Printf("ChatCompletion error: %v", err)
		return ""
	}
	if pollTool.WasSent() || imageTool.WasSent() {
		return actionOnlyReplyToken
	}

	return resp
}
