package selfprompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	chattools "github.com/focusshifter/muxgoob/internal/tools"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/facts"
)

type SelfPromptPlugin struct {
	config                registry.SelfPromptConfig
	db                    *sql.DB
	msgCounter            map[int64]int64
	refreshing            map[int64]bool
	refreshStartMessageID map[int64]int
	mutex                 sync.RWMutex
	testMode              bool // Flag to preserve counter assertions in existing tests
}

func init() {
	registry.RegisterPlugin(&SelfPromptPlugin{
		msgCounter:            make(map[int64]int64),
		refreshing:            make(map[int64]bool),
		refreshStartMessageID: make(map[int64]int),
		testMode:              false,
	})
}

// SetTestMode sets the test mode flag to prevent counter reset during tests
func (p *SelfPromptPlugin) SetTestMode(enabled bool) {
	p.testMode = enabled
}

func (p *SelfPromptPlugin) Start(config interface{}) {
	// Initialize database connection
	p.db = database.DB
	// Load configuration
	p.config = registry.Config.SelfPromptConfig
	promptmgr.EnsureTables()

	// Create settings table if not exists
	_, err := p.db.Exec(`
		CREATE TABLE IF NOT EXISTS plugin_settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER,
			plugin_name TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			UNIQUE(chat_id, plugin_name, key)
		)
	`)
	if err != nil {
		log.Printf("[selfprompt] Error creating plugin_settings table: %v", err)
	}

	// Start cleanup goroutine
	go p.cleanupRoutine()
}

func (p *SelfPromptPlugin) Process(message *telebot.Message) {
	if message.Text == "" {
		return
	}

	// Handle commands
	if strings.HasPrefix(message.Text, "!selfprompt") {
		p.handleCommand(message)
		return
	}

	// Check if enabled for this chat
	enabled, interval := p.getChatSettings(message.Chat.ID)
	if !enabled {
		return
	}

	// Check if it's time to update the prompt or if there's no prompt yet.
	// The prompt lookup is deliberately outside the lock because it touches SQLite.
	currentPrompt, err := promptmgr.GetCurrentPrompt(message.Chat.ID, true)

	// A message dispatcher goroutine is created for every incoming message. Keep a
	// per-chat watermark so messages that arrive while a refresh runs count toward
	// the *next* refresh, but cannot start duplicate concurrent refreshes.
	p.mutex.Lock()
	if p.msgCounter == nil {
		p.msgCounter = make(map[int64]int64)
	}
	if p.refreshing == nil {
		p.refreshing = make(map[int64]bool)
	}
	if p.refreshStartMessageID == nil {
		p.refreshStartMessageID = make(map[int64]int)
	}

	watermark := p.refreshStartMessageID[message.Chat.ID]
	// Message ID zero is used by a few synthetic/test messages, for which arrival
	// order is the only available ordering signal.
	if message.ID == 0 || watermark == 0 || message.ID > watermark {
		p.msgCounter[message.Chat.ID]++
	}
	count := p.msgCounter[message.Chat.ID]

	reason := ""
	switch {
	case err != nil:
		reason = "prompt_lookup_error"
	case currentPrompt == "":
		reason = "missing_prompt"
	case count >= interval:
		reason = "interval"
	}

	shouldRefresh := reason != "" && !p.refreshing[message.Chat.ID]
	if shouldRefresh {
		p.refreshing[message.Chat.ID] = true
		p.refreshStartMessageID[message.Chat.ID] = message.ID
		if !p.testMode {
			p.msgCounter[message.Chat.ID] = 0
		}
	}
	p.mutex.Unlock()

	if !shouldRefresh {
		return
	}

	log.Printf("[selfprompt] Starting refresh for chat %d: reason=%s count=%d interval=%d watermark_message_id=%d", message.Chat.ID, reason, count, interval, message.ID)
	defer func() {
		p.mutex.Lock()
		p.refreshing[message.Chat.ID] = false
		queued := p.msgCounter[message.Chat.ID]
		watermark := p.refreshStartMessageID[message.Chat.ID]
		p.mutex.Unlock()
		log.Printf("[selfprompt] Finished refresh for chat %d: queued_next=%d watermark_message_id=%d", message.Chat.ID, queued, watermark)
	}()
	p.updatePrompt(message.Chat.ID)
}

func (p *SelfPromptPlugin) handleCommand(message *telebot.Message) {
	parts := strings.Fields(message.Text)
	if len(parts) < 2 {
		// Show current settings
		enabled, interval := p.getChatSettings(message.Chat.ID)
		status := "disabled"
		if enabled {
			status = "enabled"
		}
		registry.Bot.Reply(message, fmt.Sprintf("Self-prompt is %s for this chat (interval: %d messages)", status, interval))
		return
	}

	// Check permissions
	isOwner := message.Sender.Username == registry.Config.OwnerUsername
	if !isOwner {
		registry.Bot.Reply(message, "Only chat administrators can change self-prompt settings")
		return
	}
	isOwnerPrivateChat := message.Chat.Type == telebot.ChatPrivate && isOwner

	// Handle global commands (owner only)
	if strings.HasPrefix(parts[1], "global") {
		if !isOwner {
			registry.Bot.Reply(message, "Only the bot owner can change global settings")
			return
		}
		switch parts[1] {
		case "global-enable":
			err := p.setGlobalEnabled(true)
			if err != nil {
				registry.Bot.Reply(message, fmt.Sprintf("Error enabling global self-prompt: %v", err))
				return
			}
			registry.Bot.Reply(message, "Self-prompt enabled globally")

		case "global-disable":
			err := p.setGlobalEnabled(false)
			if err != nil {
				registry.Bot.Reply(message, fmt.Sprintf("Error disabling global self-prompt: %v", err))
				return
			}
			registry.Bot.Reply(message, "Self-prompt disabled globally")

		case "global-interval":
			if len(parts) < 3 {
				registry.Bot.Reply(message, "Please specify interval value (in messages)")
				return
			}
			interval, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || interval < 1 {
				registry.Bot.Reply(message, "Invalid interval value. Please specify a positive number")
				return
			}
			err = p.setGlobalInterval(interval)
			if err != nil {
				registry.Bot.Reply(message, fmt.Sprintf("Error setting global interval: %v", err))
				return
			}
			registry.Bot.Reply(message, fmt.Sprintf("Global self-prompt interval set to %d messages", interval))

		default:
			registry.Bot.Reply(message, "Unknown global command. Available commands: global-enable, global-disable, global-interval <number>")
		}
		return
	}

	// Handle chat-specific commands (admin or owner)
	switch parts[1] {
	case "force":
		targetChatID := message.Chat.ID
		if isOwnerPrivateChat && len(parts) >= 3 {
			parsedChatID, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				registry.Bot.Reply(message, "Invalid chat ID. Please specify a valid numeric chat ID")
				return
			}
			targetChatID = parsedChatID
		}

		p.updatePrompt(targetChatID)
		registry.Bot.Reply(message, fmt.Sprintf("Forced self-prompt refresh for chat %d", targetChatID))

	case "enable":
		err := p.setChatEnabled(message.Chat.ID, true)
		if err != nil {
			registry.Bot.Reply(message, fmt.Sprintf("Error enabling self-prompt: %v", err))
			return
		}
		registry.Bot.Reply(message, "Self-prompt enabled for this chat")

	case "disable":
		err := p.setChatEnabled(message.Chat.ID, false)
		if err != nil {
			registry.Bot.Reply(message, fmt.Sprintf("Error disabling self-prompt: %v", err))
			return
		}
		registry.Bot.Reply(message, "Self-prompt disabled for this chat")

	case "interval":
		if len(parts) < 3 {
			registry.Bot.Reply(message, "Please specify interval value (in messages)")
			return
		}
		interval, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || interval < 1 {
			registry.Bot.Reply(message, "Invalid interval value. Please specify a positive number")
			return
		}
		err = p.setChatInterval(message.Chat.ID, interval)
		if err != nil {
			registry.Bot.Reply(message, fmt.Sprintf("Error setting interval: %v", err))
			return
		}
		registry.Bot.Reply(message, fmt.Sprintf("Self-prompt interval set to %d messages", interval))

	default:
		var availableCommands string
		if isOwner {
			availableCommands = "force [chat_id], enable, disable, interval <number>, global-enable, global-disable, global-interval <number>"
		} else {
			availableCommands = "force, enable, disable, interval <number>"
		}
		registry.Bot.Reply(message, fmt.Sprintf("Unknown command. Available commands: %s", availableCommands))
	}
}

func (p *SelfPromptPlugin) getChatSettings(chatID int64) (enabled bool, interval int64) {
	// First check global enabled state - if globally disabled, override chat settings
	var globalEnabledStr string
	err := p.db.QueryRow(`
		SELECT value FROM plugin_settings
		WHERE plugin_name = 'selfprompt' AND key = 'enabled'
		  AND chat_id IS NULL`,
	).Scan(&globalEnabledStr)
	if err != sql.ErrNoRows {
		if err != nil {
			log.Printf("[selfprompt] Error getting global enabled setting: %v", err)
		} else if globalEnabledStr == "false" {
			// If globally disabled, override everything
			return false, 0
		}
	}

	// Get chat-specific enabled state
	var chatEnabledStr string
	err = p.db.QueryRow(`
		SELECT value FROM plugin_settings
		WHERE plugin_name = 'selfprompt' AND key = 'enabled'
		  AND chat_id = ?`,
		chatID).Scan(&chatEnabledStr)
	if err == sql.ErrNoRows {
		enabled = true // Default to enabled if no setting
	} else if err != nil {
		log.Printf("[selfprompt] Error getting chat enabled setting: %v", err)
		enabled = true
	} else {
		enabled = chatEnabledStr == "true"
	}

	// Get interval - first try chat-specific setting, then global
	var intervalStr string
	err = p.db.QueryRow(`
		SELECT value FROM plugin_settings
		WHERE plugin_name = 'selfprompt' AND key = 'interval'
		  AND (chat_id = ? OR chat_id IS NULL)
		ORDER BY chat_id NULLS LAST
		LIMIT 1`,
		chatID).Scan(&intervalStr)
	if err == sql.ErrNoRows {
		interval = 100 // Default interval
	} else if err != nil {
		log.Printf("[selfprompt] Error getting interval setting: %v", err)
		interval = 100
	} else {
		interval, err = strconv.ParseInt(intervalStr, 10, 64)
		if err != nil {
			log.Printf("[selfprompt] Error parsing interval value: %v", err)
			interval = 100
		}
	}

	return enabled, interval
}

func (p *SelfPromptPlugin) setPluginSetting(chatID *int64, key string, value string) error {
	// Handle global settings (NULL chat_id) specially
	if chatID == nil {
		// First check if a global setting already exists
		var count int
		err := p.db.QueryRow(`
			SELECT COUNT(*) FROM plugin_settings
			WHERE chat_id IS NULL AND plugin_name = 'selfprompt' AND key = ?`,
			key).Scan(&count)

		if err != nil {
			log.Printf("[selfprompt] Error checking for existing global setting %s: %v", key, err)
			return err
		}

		// If it exists, update it
		if count > 0 {
			_, err = p.db.Exec(`
				UPDATE plugin_settings SET value = ?
				WHERE chat_id IS NULL AND plugin_name = 'selfprompt' AND key = ?`,
				value, key)
		} else {
			// Otherwise insert a new row
			_, err = p.db.Exec(`
				INSERT INTO plugin_settings (chat_id, plugin_name, key, value)
				VALUES (NULL, 'selfprompt', ?, ?)`,
				key, value)
		}

		return err
	}

	// For chat-specific settings, we can use the UNIQUE constraint
	_, err := p.db.Exec(`
		INSERT INTO plugin_settings (chat_id, plugin_name, key, value)
		VALUES (?, 'selfprompt', ?, ?)
		ON CONFLICT(chat_id, plugin_name, key) DO UPDATE SET value = ?`,
		chatID, key, value, value)
	return err
}

func (p *SelfPromptPlugin) setChatEnabled(chatID int64, enabled bool) error {
	return p.setPluginSetting(&chatID, "enabled", strconv.FormatBool(enabled))
}

func (p *SelfPromptPlugin) setGlobalEnabled(enabled bool) error {
	return p.setPluginSetting(nil, "enabled", strconv.FormatBool(enabled))
}

func (p *SelfPromptPlugin) setChatInterval(chatID int64, interval int64) error {
	return p.setPluginSetting(&chatID, "interval", strconv.FormatInt(interval, 10))
}

func (p *SelfPromptPlugin) setGlobalInterval(interval int64) error {
	return p.setPluginSetting(nil, "interval", strconv.FormatInt(interval, 10))
}

func (p *SelfPromptPlugin) cleanupRoutine() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		p.mutex.Lock()
		// Clear counters for non-existent chats
		for chatID := range p.msgCounter {
			var exists bool
			err := p.db.QueryRow("SELECT 1 FROM chats WHERE id = ?", chatID).Scan(&exists)
			if err == sql.ErrNoRows {
				delete(p.msgCounter, chatID)
			}
		}
		p.mutex.Unlock()
	}
}

func (p *SelfPromptPlugin) retrieveHistoryForChat(chatID int64, messageCount int) []telebot.Message {
	rows, err := p.db.Query(
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
			log.Printf("Error scanning message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
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
		parentMessages := retrieveMessagesByIDs(p.db, chatID, replyParentIDs)
		messages = append(messages, parentMessages...)
	}

	// Sort by ID for consistent order
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Unixtime < messages[j].Unixtime
	})

	log.Printf("Retrieved %v messages", len(messages))

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
		log.Printf("Error retrieving parent messages: %v", err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			log.Printf("Error scanning parent message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("Error unmarshaling parent message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages
}

// Variable to hold the prompt/fact generation functions for testing.
var generateNewPromptFunc func(p *SelfPromptPlugin, history string, currentPrompt string) string
var generateUserFactsFunc func(p *SelfPromptPlugin, chatID int64, user activeChatUser, history string, currentFacts string) string
var consolidatePersonFactsFunc func(chatID int64, userName string, currentFacts string) string

func (p *SelfPromptPlugin) updatePrompt(chatID int64) {
	log.Printf("[selfprompt] Updating prompt for chat %d", chatID)

	if p.shouldBootstrapChat(chatID) {
		p.bootstrapChat(chatID, registry.Config.ChatGptHistoryDepth)
		return
	}

	messages := p.retrieveHistoryForChat(chatID, registry.Config.ChatGptHistoryDepth)
	p.updatePromptFromMessages(chatID, messages)
}

func (p *SelfPromptPlugin) updatePromptFromMessages(chatID int64, messages []telebot.Message) {
	if len(messages) == 0 {
		return
	}

	currentPrompt, err := promptmgr.GetCurrentPrompt(chatID, false)
	if err != nil {
		log.Printf("[selfprompt] Error getting current prompt for chat %d: %v", chatID, err)
		return
	}
	if err := chatmemory.EnsureSchema(p.db); err != nil {
		log.Printf("[selfprompt] Error ensuring structured memory schema for chat %d: %v", chatID, err)
		return
	}

	history := p.generateChatGptHistory(messages)
	p.updatePersonFacts(chatID, messages, history)

	var newPrompt string
	if generateNewPromptFunc != nil {
		newPrompt = generateNewPromptFunc(p, history, currentPrompt)
	} else {
		newPrompt = p.generateNewPrompt(chatID, history, currentPrompt)
	}

	newPrompt = strings.TrimSpace(newPrompt)
	if newPrompt == "" {
		return
	}
	var stableMemory []string
	if chatmemory.HasLegacyStableContext(newPrompt) {
		parsedPrompt := facts.ParseChatPrompt(newPrompt)
		stableMemory = append([]string(nil), parsedPrompt.StableContext...)
		parsedPrompt.StableContext = nil
		newPrompt = facts.RenderChatPrompt(parsedPrompt)
	}

	err = database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			repo := chatmemory.NewRepository(p.db)
			for _, body := range stableMemory {
				if _, _, memoryErr := repo.AddTx(context.Background(), tx, chatmemory.Entry{
					ChatID: chatID, Kind: chatmemory.ChatLore, Body: body, SourceType: "selfprompt",
				}); memoryErr != nil {
					return memoryErr
				}
			}

			var nextVersion int
			err = tx.QueryRow(`
				SELECT COALESCE(MAX(version) + 1, 1) FROM prompts WHERE chat_id = ?`,
				chatID).Scan(&nextVersion)
			if err != nil {
				return err
			}

			_, err = tx.Exec(`
				INSERT INTO prompts (chat_id, version, prompt, created_at)
				VALUES (?, ?, ?, ?)`,
				chatID, nextVersion, newPrompt, time.Now().Unix())
			return err
		})
	})

	if err != nil {
		log.Printf("[selfprompt] Error updating prompt for chat %d: %v", chatID, err)
	}
}

func (p *SelfPromptPlugin) shouldBootstrapChat(chatID int64) bool {
	var promptCount int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM prompts WHERE chat_id = ?`, chatID).Scan(&promptCount)
	if err != nil {
		log.Printf("[selfprompt] Error checking prompts for chat %d: %v", chatID, err)
		return false
	}
	if promptCount > 0 {
		return false
	}

	var factCount int
	if chatmemory.IsCutover(context.Background(), p.db, chatID) {
		err = p.db.QueryRow(`SELECT COUNT(*) FROM memory_entries WHERE chat_id=? AND kind IN ('chat_lore','person_fact') AND status='active'`, chatID).Scan(&factCount)
	} else {
		err = p.db.QueryRow(`SELECT COUNT(*) FROM person_facts WHERE chat_id = ?`, chatID).Scan(&factCount)
	}
	if err != nil {
		log.Printf("[selfprompt] Error checking memory for chat %d: %v", chatID, err)
		return false
	}

	return factCount == 0
}

func (p *SelfPromptPlugin) bootstrapChat(chatID int64, batchSize int) {
	if batchSize <= 0 {
		batchSize = 20
	}

	var totalMessages int
	err := p.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ?`, chatID).Scan(&totalMessages)
	if err != nil {
		log.Printf("[selfprompt] Error counting messages for bootstrap in chat %d: %v", chatID, err)
		return
	}
	if totalMessages == 0 {
		return
	}

	log.Printf("[selfprompt] Bootstrapping chat %d across %d messages", chatID, totalMessages)
	for offset := 0; offset < totalMessages; offset += batchSize {
		messages := p.retrieveHistoryBatch(chatID, batchSize, offset)
		if len(messages) == 0 {
			continue
		}
		p.updatePromptFromMessages(chatID, messages)
	}
}

func (p *SelfPromptPlugin) retrieveHistoryBatch(chatID int64, limit int, offset int) []telebot.Message {
	rows, err := p.db.Query(
		`SELECT data FROM messages
		WHERE chat_id = ?
		ORDER BY unixtime ASC LIMIT ? OFFSET ?`,
		chatID, limit, offset)
	if err != nil {
		log.Printf("Error retrieving chat history batch: %v", err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			log.Printf("Error scanning message batch row: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("Error unmarshaling batched message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages
}

type ChatMessage struct {
	SenderID   int64
	SenderName string
	Text       string
	Timestamp  int64
}

type activeChatUser struct {
	ID   int64
	Name string
}

func (p *SelfPromptPlugin) generateChatGptHistory(messages []telebot.Message) string {
	var history strings.Builder

	for _, message := range messages {
		if message.Sender == nil {
			continue
		}
		history.WriteString(fmt.Sprintf("%s: %s\n", displayName(message.Sender), message.Text))
	}

	return history.String()
}

func displayName(user *telebot.User) string {
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
	return fmt.Sprintf("user_%d", user.ID)
}

func collectActiveUsers(messages []telebot.Message) []activeChatUser {
	seen := make(map[int64]struct{})
	users := make([]activeChatUser, 0)
	for _, message := range messages {
		if message.Sender == nil || message.Sender.ID == 0 {
			continue
		}
		userID := int64(message.Sender.ID)
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		users = append(users, activeChatUser{ID: userID, Name: displayName(message.Sender)})
	}
	sort.Slice(users, func(i, j int) bool {
		return users[i].ID < users[j].ID
	})
	return users
}

func filterMessagesByUser(messages []telebot.Message, userID int64) []telebot.Message {
	filtered := make([]telebot.Message, 0)
	for _, message := range messages {
		if message.Sender == nil || int64(message.Sender.ID) != userID {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func (p *SelfPromptPlugin) updatePersonFacts(chatID int64, messages []telebot.Message, _ string) {
	if err := chatmemory.EnsureSchema(p.db); err != nil {
		log.Printf("[selfprompt] Error ensuring structured memory schema for person facts in chat %d: %v", chatID, err)
		return
	}
	activeUsers := collectActiveUsers(messages)
	for _, user := range activeUsers {
		userMessages := filterMessagesByUser(messages, user.ID)
		if len(userMessages) == 0 {
			continue
		}
		userHistory := p.generateChatGptHistory(userMessages)

		currentFacts, err := promptmgr.GetPersonFacts(chatID, user.ID)
		if err != nil {
			log.Printf("[selfprompt] Error getting person facts for chat %d user %d: %v", chatID, user.ID, err)
			continue
		}

		var newFacts string
		if generateUserFactsFunc != nil {
			newFacts = generateUserFactsFunc(p, chatID, user, userHistory, currentFacts)
		} else {
			newFacts = p.generateUserFacts(chatID, user, userHistory, currentFacts)
		}

		newFacts = facts.EnforcePersonFactsBudgets(newFacts)
		if newFacts == "" || newFacts == facts.EnforcePersonFactsBudgets(currentFacts) {
			continue
		}

		err = promptmgr.SavePersonFacts(chatID, int64(user.ID), newFacts)
		if err != nil {
			log.Printf("[selfprompt] Error saving person facts for chat %d user %d: %v", chatID, user.ID, err)
		}
	}
}

func buildOpenAIClient(chatID *int64) (chattools.ChatCompletionCreator, string) {
	return buildOpenAIClientForModel(chatID, "")
}

func buildOpenAIClientForModel(chatID *int64, requestedModel string) (chattools.ChatCompletionCreator, string) {
	model := strings.TrimSpace(requestedModel)

	aiProvider := registry.GetAiProvider(chatID)
	switch aiProvider {
	case "openrouter":
		config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		if model == "" {
			model = GetModel(chatID)
		}
		if model == "" {
			model = registry.GetAiModel(chatID)
		}
		return openai.NewClientWithConfig(config), model
	case "openai-codex":
		if model == "" {
			model = GetModel(chatID)
		}
		if model == "" {
			model = strings.TrimSpace(registry.GetAiModel(chatID))
		}
		if model == "" {
			model = "gpt-5.4"
		}
		modelInfo := openaicodex.NormalizeConfiguredModel(model)
		if requestedModel != "" {
			modelInfo = openaicodex.NormalizeConfiguredModel(requestedModel)
		}
		if modelInfo.UseCodex {
			fallbackConfig := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
			fallbackConfig.BaseURL = "https://openrouter.ai/api/v1"
			return openaicodex.NewClient(openaicodex.WithFallbackClient(openai.NewClientWithConfig(fallbackConfig))), modelInfo.RawModel
		}
		config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		return openai.NewClientWithConfig(config), modelInfo.OpenRouterModel
	default:
		config := openai.DefaultConfig(registry.Config.OpenaiApiKey)
		if model == "" {
			model = GetModel(chatID)
		}
		if model == "" {
			model = "gpt-4o-mini"
		}
		return openai.NewClientWithConfig(config), model
	}
}

func shouldConsolidateFacts(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	bulletCount := strings.Count(trimmed, "\n- ")
	return len(trimmed) >= defaultCompactChars || bulletCount >= defaultCompactBullets
}

func bootstrapFactsRequired(currentFacts string) bool {
	return strings.TrimSpace(currentFacts) == ""
}

func consolidatePersonFacts(chatID int64, userName string, currentFacts string) string {
	client, model := buildOpenAIClientForModel(&chatID, "")
	systemMsg := `You consolidate a person's profile by merging overlapping bullets while preserving all durable meaning.`

	for attempt := 1; attempt <= 2; attempt++ {
		userMsg := fmt.Sprintf(`
Consolidate this person's stored profile into a tighter version.

Rules:
1. Preserve durable meaning from the current profile. Do not invent facts.
2. Merge overlapping bullets when they describe the same broader interest or trait.
3. Prefer compact grouped bullets over many one-title bullets when that summary is faithful.
4. Remove redundant repetition.
5. Do not start bullets with the person's name, username, or phrases like '%s has' or '%s is'.
6. Output exactly these English headings in this order:
Identity:
Interests:
7. Under each heading, use only '- ' bullets.
8. Keep only concise factual bullets that help future replies.
9. Do not add commentary or text outside the dossier.

Current profile:
%s
`, userName, userName, currentFacts)
		if attempt == 2 {
			userMsg += "\nYour previous answer was invalid. Retry once and return only the exact dossier structure.\n"
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: model,
			Messages: []openai.ChatCompletionMessage{
				{Role: "system", Content: systemMsg},
				{Role: "user", Content: userMsg},
			},
		})
		cancel()
		if err != nil {
			log.Printf("[selfprompt] Error consolidating facts for chat %d: %v", chatID, err)
			return currentFacts
		}
		if len(resp.Choices) == 0 {
			return currentFacts
		}

		evaluation := facts.EvaluatePersonFacts(currentFacts, resp.Choices[0].Message.Content)
		if evaluation.Accepted {
			if len(strings.TrimSpace(evaluation.Value)) >= len(strings.TrimSpace(currentFacts)) {
				return currentFacts
			}
			return evaluation.Value
		}
		if evaluation.Retryable && attempt == 1 {
			continue
		}
		return currentFacts
	}

	return currentFacts
}

func consolidateChatPrompt(chatID int64, currentPrompt string) string {
	client, model := buildOpenAIClientForModel(&chatID, "")
	systemMsg := `You consolidate chat reply guidance by removing repetition and keeping only the highest-value durable instructions.`

	for attempt := 1; attempt <= 2; attempt++ {
		userMsg := fmt.Sprintf(`
Consolidate this chat prompt into a tighter version.

Rules:
1. Preserve durable reply guidance and stable context. Do not invent new facts.
2. Merge overlapping bullets and remove duplicate wording.
3. Keep this exact structure and order:
Reply style:
Stable context:
Avoid:
4. Under each heading, use only '- ' bullets.
5. Budget:
- Reply style: max 5 bullets
- Stable context: max 6 bullets
- Avoid: max 4 bullets
6. Prefer canonical wording over repeated paraphrases.
7. Do not include raw delta syntax, arrows, or notes like 'new guidance'.
8. Do not let one recent exchange dominate the prompt.
9. Keep bullets compact and useful for future replies.

Current prompt:
%s
`, currentPrompt)
		if attempt == 2 {
			userMsg += "\nYour previous answer was invalid. Retry once and return only the exact prompt structure.\n"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{Model: model, Messages: []openai.ChatCompletionMessage{{Role: "system", Content: systemMsg}, {Role: "user", Content: userMsg}}})
		cancel()
		if err != nil || len(resp.Choices) == 0 {
			return currentPrompt
		}
		candidate := facts.EnforceChatPromptBudgets(facts.ParseChatPrompt(resp.Choices[0].Message.Content))
		value := facts.RenderChatPrompt(candidate)
		if strings.TrimSpace(value) == "" {
			return currentPrompt
		}
		return value
	}
	return currentPrompt
}

func (p *SelfPromptPlugin) generateNewPrompt(chatID int64, history string, currentPrompt string) string {
	client, model := buildOpenAIClient(&chatID)

	systemMsg := `You refine durable chat-specific reply guidance.`

	for attempt := 1; attempt <= 2; attempt++ {
		userMsg := fmt.Sprintf(`
Analyze the provided chat history and identify new or updated reply guidance for this chat.

Rules:
1. Output ONLY new or updated guidance. Do not reproduce the full chat profile.
2. Use '+ ' for new reply guidance, durable context, or things the bot should avoid.
3. Use '~ old guidance -> new guidance' when a current instruction should be refined.
4. Output under these exact English headings when needed:
Reply style:
Stable context:
Avoid:
5. Omit headings that have no changes.
6. Do not include per-person profiles or headings for specific users.
7. Prefer the main language of the chat.
8. Focus on durable guidance that improves future replies, not generic topic summaries.
9. Stable context should capture recurring lore, games, rituals, memes, or running situations only when they matter for replies.
10. Reply style should describe how the bot should respond here, not list discussion subjects.
11. Avoid should capture failure modes, misreads, or tones the bot should avoid.
12. Absence of a topic in recent messages does not mean it should be removed.
13. If nothing new or durable emerged, output exactly: NO_CHANGES
14. Do not add commentary, explanations, or text outside the delta format.

Current chat profile for context (do not reproduce it):
%s

Chat history:
%s
`, currentPrompt, history)
		if attempt == 2 {
			userMsg += "\nYour previous answer was invalid. Retry once and return only a valid delta or NO_CHANGES.\n"
		}

		resp, err := client.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model: model,
				Messages: []openai.ChatCompletionMessage{
					{Role: "system", Content: systemMsg},
					{Role: "user", Content: userMsg},
				},
			},
		)
		if err != nil {
			log.Printf("[selfprompt] Error generating new prompt: %v", err)
			return currentPrompt
		}
		if len(resp.Choices) == 0 {
			log.Printf("[selfprompt] No prompt generated by GPT")
			return currentPrompt
		}

		raw := strings.TrimSpace(resp.Choices[0].Message.Content)
		if raw == "" {
			log.Printf("[selfprompt] Empty prompt generated by GPT")
			return currentPrompt
		}
		if facts.IsNoChanges(raw) {
			return currentPrompt
		}

		delta, accepted, retryable, reason := facts.EvaluateChatDelta(raw)
		if !accepted {
			if retryable && attempt == 1 {
				log.Printf("[selfprompt] Retrying prompt generation after invalid output: %s", reason)
				continue
			}
			log.Printf("[selfprompt] Skipping prompt update after invalid output: %s", reason)
			return currentPrompt
		}

		current := facts.ParseChatPrompt(currentPrompt)
		delta = facts.SanitizeChatDelta(delta)
		delta = facts.FilterChatDelta(current, delta)
		merged := facts.ApplyChatDelta(current, delta)
		merged = facts.EnforceChatPromptBudgets(merged)
		newPrompt := facts.RenderChatPrompt(merged)
		if strings.TrimSpace(newPrompt) == "" {
			return currentPrompt
		}
		newPrompt = consolidateChatPrompt(chatID, newPrompt)

		log.Printf("[selfprompt] Generated new prompt: %s", newPrompt)
		return newPrompt
	}

	return currentPrompt
}

func (p *SelfPromptPlugin) generateUserFacts(chatID int64, user activeChatUser, history string, currentFacts string) string {
	client, model := buildOpenAIClient(&chatID)

	systemMsg := `You extract new evidence about a chat member from their recent messages.`
	requireBootstrap := bootstrapFactsRequired(currentFacts)

	for attempt := 1; attempt <= 2; attempt++ {
		userMsg := fmt.Sprintf(`
Analyze the recent messages written by exactly one person: %s.

Rules:
1. Output ONLY new or updated facts. Do not reproduce the full profile.
2. Use '+ ' for new facts not already in the current profile.
3. Use '~ old fact -> new fact' when a current fact should be refined.
4. Use only what this person says in their own messages below. Do not infer facts from what other people say about them.
5. Focus on durable traits, interests, preferences, habits, and self-stated facts.
6. Ignore one-off jokes or fleeting topics unless they look stable.
7. Prefer the main language of the chat.
8. Absence of a topic in recent messages does not mean it should be removed.
9. Write bullets as concise facts or preferences about the person. Do not start bullets with the person's name, username, or phrases like '%s has' or '%s is'.
10. Output headings only when they have changes, using these exact English headings:
Identity:
Interests:
11. If the recent messages add nothing durable, output exactly: NO_CHANGES
12. Do not add commentary, explanations, markdown emphasis, update notes, or any text outside the delta format.

Current profile for context (do not reproduce it):
%s

Recent messages from %s:
%s
`, user.Name, user.Name, user.Name, currentFacts, user.Name, history)
		if requireBootstrap {
			userMsg += "\nThe current profile is empty. Bootstrap an initial profile from any durable evidence you can find. Only return NO_CHANGES if there is truly no stable personal information at all in these messages.\n"
		}
		if attempt == 2 {
			userMsg += "\nYour previous answer was invalid. Retry once and return only a valid delta or NO_CHANGES.\n"
		}

		resp, err := client.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model: model,
				Messages: []openai.ChatCompletionMessage{
					{Role: "system", Content: systemMsg},
					{Role: "user", Content: userMsg},
				},
			},
		)
		if err != nil {
			log.Printf("[selfprompt] Error generating user facts for chat %d user %d: %v", chatID, user.ID, err)
			return currentFacts
		}
		if len(resp.Choices) == 0 {
			return currentFacts
		}

		raw := strings.TrimSpace(resp.Choices[0].Message.Content)
		if facts.IsNoChanges(raw) {
			if requireBootstrap && attempt == 1 {
				log.Printf("[selfprompt] Retrying facts generation for chat %d user %d because profile is empty and model returned NO_CHANGES", chatID, user.ID)
				continue
			}
			return currentFacts
		}

		delta, accepted, retryable, reason := facts.EvaluateDelta(raw)
		if accepted {
			current := facts.ParseDossier(currentFacts)
			delta = facts.SanitizeDeltaForPerson(delta, user.Name)
			delta = facts.FilterDeltaForDossier(current, delta)
			if delta == nil || len(delta.Identity)+len(delta.Interests) == 0 {
				return currentFacts
			}
			merged := facts.EnforceDossierBudgets(facts.ApplyDelta(current, delta))
			candidate := facts.RenderDossier(merged)
			if shouldConsolidateFacts(candidate) {
				consolidator := consolidatePersonFacts
				if consolidatePersonFactsFunc != nil {
					consolidator = consolidatePersonFactsFunc
				}
				candidate = facts.EnforcePersonFactsBudgets(consolidator(chatID, user.Name, candidate))
			}
			evaluation := facts.EvaluatePersonFacts(currentFacts, candidate)
			if evaluation.Accepted {
				return evaluation.Value
			}
			log.Printf("[selfprompt] Skipping facts update for chat %d user %d after merge failed safety check: %s", chatID, user.ID, evaluation.Reason)
			return currentFacts
		}
		if retryable && attempt == 1 {
			log.Printf("[selfprompt] Retrying facts generation for chat %d user %d after invalid output: %s", chatID, user.ID, reason)
			continue
		}

		log.Printf("[selfprompt] Skipping facts update for chat %d user %d after invalid output: %s", chatID, user.ID, reason)
		return currentFacts
	}

	return currentFacts
}
