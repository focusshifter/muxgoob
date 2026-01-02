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
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
)

type SelfPromptPlugin struct {
	config     registry.SelfPromptConfig
	db         *sql.DB
	msgCounter map[int64]int64
	mutex      sync.RWMutex
	testMode   bool // Flag to indicate test mode - prevents counter reset
}

func init() {
	registry.RegisterPlugin(&SelfPromptPlugin{
		msgCounter: make(map[int64]int64),
		testMode:   false,
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

	// Initialize message counter map if it's nil
	if p.msgCounter == nil {
		p.msgCounter = make(map[int64]int64)
	}

	// Update message counter
	p.mutex.Lock()
	p.msgCounter[message.Chat.ID]++
	count := p.msgCounter[message.Chat.ID]
	p.mutex.Unlock()

	// Check if it's time to update the prompt or if there's no prompt yet
	currentPrompt, err := promptmgr.GetCurrentPrompt(message.Chat.ID, true)
	if err != nil || currentPrompt == "" || count >= interval {
		// Save the current counter value before updating the prompt
		// This allows tests to verify the counter was incremented correctly
		currentCount := count

		// Update the prompt
		p.updatePrompt(message.Chat.ID)

		// Only reset the counter if it hasn't been changed during updatePrompt
		// and we're not in test mode
		p.mutex.Lock()
		if !p.testMode && p.msgCounter[message.Chat.ID] == currentCount {
			p.msgCounter[message.Chat.ID] = 0
		}
		p.mutex.Unlock()
	}
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
			availableCommands = "enable, disable, interval <number>, global-enable, global-disable, global-interval <number>"
		} else {
			availableCommands = "enable, disable, interval <number>"
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
		`SELECT data FROM messages 
		WHERE chat_id = ? 
		ORDER BY unixtime DESC LIMIT ?`,
		chatID, messageCount)
	if err != nil {
		log.Printf("Error retrieving chat history: %v", err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			log.Printf("Error scanning message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	// Sort by ID for consistent order
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Unixtime < messages[j].Unixtime
	})

	log.Printf("Retrieved %v messages", len(messages))

	return messages
}

// Variable to hold the generateNewPrompt function for testing
var generateNewPromptFunc func(p *SelfPromptPlugin, history string, currentPrompt string) string

func (p *SelfPromptPlugin) updatePrompt(chatID int64) {
	// Log the function entry
	log.Printf("[selfprompt] Updating prompt for chat %d", chatID)

	// Get last X messages from the chat
	messages := p.retrieveHistoryForChat(chatID, registry.Config.ChatGptHistoryDepth)

	// Get current prompt
	currentPrompt, err := promptmgr.GetCurrentPrompt(chatID, false)
	if err != nil {
		log.Printf("[selfprompt] Error getting current prompt for chat %d: %v", chatID, err)
		return
	}

	history := p.generateChatGptHistory(messages)

	// Analyze messages and generate new prompt
	var newPrompt string
	if generateNewPromptFunc != nil {
		// Use the mock function if provided (for testing)
		newPrompt = generateNewPromptFunc(p, history, currentPrompt)
	} else {
		// Use the real implementation
		newPrompt = p.generateNewPrompt(history, currentPrompt)
	}

	// Update the prompt in the database
	err = database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			// Get next version
			var nextVersion int
			err = tx.QueryRow(`
				SELECT COALESCE(MAX(version) + 1, 1) FROM prompts WHERE chat_id = ?`,
				chatID).Scan(&nextVersion)
			if err != nil {
				return err
			}

			// Insert new prompt
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

type ChatMessage struct {
	SenderID   int64
	SenderName string
	Text       string
	Timestamp  int64
}

func (p *SelfPromptPlugin) generateChatGptHistory(messages []telebot.Message) string {
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

func (p *SelfPromptPlugin) generateNewPrompt(history string, currentPrompt string) string {
	// Create GPT client
	var config openai.ClientConfig
	var model string

	// Get AI provider from database with fallback to config.yml
	// Using nil for chatID to get the global setting
	aiProvider := registry.GetAiProvider(nil)

	if aiProvider == "openrouter" {
		config = openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		// Get AI model from database with fallback to config.yml
		model = registry.GetAiModel(nil)
	} else {
		config = openai.DefaultConfig(registry.Config.OpenaiApiKey)
		model = "gpt-4o-mini"
	}

	client := openai.NewClientWithConfig(config)

	systemMsg := `You refine bot system prompts based on chat context.`
	userMsg := `
	Analyze the provided chat history and refine the current system prompt to produce a concise, informative new bot system prompt that:
1. Identifies key discussion topics from the chat history.  
2. For every chat member:  
   - Assesses user relationships, personality traits, interests, and preferences.  
   - Lists findings under a header "[USERNAME]: ".
3. Preserves and refines critical personality traits or instructions from the current prompt, such as analytical precision and prompt engineering focus.  
4. Ensures clarity and brevity.  
5. Preferrably written in the main language of the chat, such as English or Russian.

Do not skip ANY chat member. Do not remove ANY existing chat members from the result even of they are inactive.

Output only the new bot system prompt text. Do not include meta-instructions, role labels, or phrases like "You are a prompt engineer".

**Discussion Topics:**  
- [List key topics from chat history, e.g., "Programming languages", "Speed and performance"]  

**[USERNAME]:**  
- [Traits, relationships, interests, e.g., "Analytical, debates USER456, prefers Go"]  
**[USERNAME]:**  
- [Traits, relationships, interests, e.g., "Practical, counters USER123, likes Python"]\n\n`

	userMsg += `**Current Prompt to Refine:** ` + currentPrompt + `\n\n`
	userMsg += `**Chat History:**\n` + history

	// Call GPT
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

	newPrompt := resp.Choices[0].Message.Content
	if newPrompt == "" {
		log.Printf("[selfprompt] Empty prompt generated by GPT")
		return currentPrompt
	}

	log.Printf("[selfprompt] Generated new prompt: %s", newPrompt)

	return newPrompt
}
