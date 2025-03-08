package promptmgr

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

// GLOBAL_CHAT_ID is a special chat ID used for global prompts
const GLOBAL_CHAT_ID int64 = 0

// PromptMgrPlugin manages and stores prompts in the database
type PromptMgrPlugin struct{}

func init() {
	registry.RegisterPlugin(&PromptMgrPlugin{})
}

func createPromptTables() {
	// Create the prompts table
	_, err := database.DB.Exec(`
		CREATE TABLE IF NOT EXISTS prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			prompt TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(chat_id, version)
		);
	`)
	if err != nil {
		log.Fatal("Failed to create prompts table:", err)
	}
}

func (p *PromptMgrPlugin) Start(interface{}) {
	// Create prompt tables if they don't exist
	createPromptTables()
}

func (p *PromptMgrPlugin) Process(message *telebot.Message) {
	if message.Text == "" {
		return
	}

	log.Printf("[promptmgr] Received message: %q", message.Text)

	// Check if the message starts with !prompt (but not !promptmgr or other variants)
	if !strings.HasPrefix(message.Text, "!prompt ") && message.Text != "!prompt" {
		log.Printf("[promptmgr] Not a prompt command, ignoring")
		return
	}

	log.Printf("[promptmgr] Processing prompt command")
	bot := registry.Bot
	args := strings.Fields(message.Text)

	// Need at least !prompt command
	if len(args) < 1 {
		return
	}

	// Check if the user is the owner
	isOwner := message.Sender.Username == registry.Config.OwnerUsername

	// Private chat with owner can manage any chat's prompt
	isOwnerPrivateChat := message.Chat.Type == telebot.ChatPrivate && isOwner

	log.Printf("[promptmgr] Is owner: %v, Is owner private chat: %v (chat type: %v, sender: %q, owner: %q)",
		isOwner, isOwnerPrivateChat, message.Chat.Type, message.Sender.Username, registry.Config.OwnerUsername)

	// All other commands are restricted to owners only
	if !isOwner {
		bot.Send(message.Chat, "Sorry, only the bot owner can control prompts.")
		return
	}

	// Handle various command formats
	if len(args) == 1 {
		// Just !prompt - redirect to !prompt current
		log.Printf("[promptmgr] Redirecting to !prompt current for chat: %d", message.Chat.ID)
		showCurrentPrompt(message.Chat.ID, bot, message)
		return
	}

	// Handle !prompt revert command
	if args[1] == "revert" {
		log.Printf("[promptmgr] Processing revert command")
		if isOwnerPrivateChat && len(args) >= 3 {
			// Format: !prompt revert <chat_id> <version> - revert specific chat to version
			chatID, err := strconv.ParseInt(args[2], 10, 64)
			if err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Invalid chat ID: %s", args[2]))
				return
			}

			if len(args) >= 4 {
				version, err := strconv.Atoi(args[3])
				if err != nil {
					bot.Send(message.Chat, fmt.Sprintf("Invalid version number: %s", args[3]))
					return
				}
				revertPrompt(chatID, version, bot, message)
			} else {
				// List versions for this chat
				listPromptVersions(chatID, bot, message)
			}
		} else if len(args) >= 2 {
			// Format: !prompt revert <version> - revert current chat to version
			version, err := strconv.Atoi(args[2])
			if err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Invalid version number: %s", args[2]))
				return
			}
			revertPrompt(message.Chat.ID, version, bot, message)
		}
		return
	}

	// Handle !prompt current command
	if args[1] == "current" {
		if len(args) == 2 {
			// Just !prompt current - show current prompt for this chat
			log.Printf("[promptmgr] Showing current prompt for chat: %d", message.Chat.ID)
			showCurrentPrompt(message.Chat.ID, bot, message)
		} else {
			// !prompt current <new_prompt> - update current chat's prompt
			newPrompt := strings.TrimPrefix(message.Text, "!prompt current ")
			log.Printf("[promptmgr] Updating prompt for current chat %d: %q", message.Chat.ID, newPrompt)
			updatePrompt(message.Chat.ID, newPrompt, bot, message)
		}
		return
	}

	// Handle !prompt <chat_id> command
	if isOwnerPrivateChat && len(args) >= 2 {
		chatID, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			// Not a chat ID, assume it's the start of the prompt for current chat
			newPrompt := strings.TrimPrefix(message.Text, "!prompt ")
			log.Printf("[promptmgr] Updating prompt for current chat %d: %q", message.Chat.ID, newPrompt)
			updatePrompt(message.Chat.ID, newPrompt, bot, message)
			return
		}

		// Valid chat ID
		if len(args) == 2 {
			// Just !prompt <chat_id> - show current prompt for that chat
			log.Printf("[promptmgr] Showing prompt for chat: %d", chatID)
			showCurrentPrompt(chatID, bot, message)
		} else {
			// !prompt <chat_id> <new_prompt> - update that chat's prompt
			newPrompt := strings.TrimPrefix(message.Text, fmt.Sprintf("!prompt %s ", args[1]))
			log.Printf("[promptmgr] Updating prompt for chat %d: %q", chatID, newPrompt)
			updatePrompt(chatID, newPrompt, bot, message)
		}
		return
	}

	// If we get here, it's an invalid command format
	bot.Send(message.Chat, "Invalid command format. Use:\n" +
		"!prompt current - Show current chat's prompt\n" +
		"!prompt current <new_prompt> - Set current chat's prompt\n" +
		"!prompt <chat_id> - Show another chat's prompt\n" +
		"!prompt <chat_id> <new_prompt> - Set another chat's prompt")
}

func showCurrentPrompt(chatID int64, bot *registry.BotWrapper, message *telebot.Message) {
	// First check if there's a chat-specific prompt
	var prompt string
	var version int
	err := database.DB.QueryRow(`
		SELECT prompt, version FROM prompts 
		WHERE chat_id = ? 
		ORDER BY version DESC LIMIT 1`, chatID).Scan(&prompt, &version)

	// Create a prefix for the message to indicate which chat we're showing
	chatPrefix := ""
	if chatID != message.Chat.ID {
		chatPrefix = fmt.Sprintf("Chat ID %d: ", chatID)
	}

	if err == sql.ErrNoRows {
		// If no chat-specific prompt, check for global prompt
		err = database.DB.QueryRow(`
			SELECT prompt, version FROM prompts 
			WHERE chat_id = ? 
			ORDER BY version DESC LIMIT 1`, GLOBAL_CHAT_ID).Scan(&prompt, &version)

		if err == sql.ErrNoRows {
			bot.Send(message.Chat, fmt.Sprintf("%sNo prompt is set for this chat.", chatPrefix))
			return
		} else if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("%sError retrieving prompt: %s", chatPrefix, err.Error()))
			return
		}

		bot.Send(message.Chat, fmt.Sprintf("%sCurrent global prompt (version %d):\n\n%s", 
			chatPrefix, version, prompt))
		return
	} else if err != nil {
		bot.Send(message.Chat, fmt.Sprintf("%sError retrieving prompt: %s", chatPrefix, err.Error()))
		return
	}

	bot.Send(message.Chat, fmt.Sprintf("%sCurrent chat prompt (version %d):\n\n%s", 
		chatPrefix, version, prompt))
}

func listPromptVersions(chatID int64, bot *registry.BotWrapper, message *telebot.Message) {
	rows, err := database.DB.Query(`
		SELECT version, created_at, 
		SUBSTR(prompt, 1, 50) || CASE WHEN LENGTH(prompt) > 50 THEN '...' ELSE '' END AS prompt_preview
		FROM prompts 
		WHERE chat_id = ? 
		ORDER BY version DESC`, chatID)

	if err != nil {
		bot.Send(message.Chat, "Error retrieving prompt versions: "+err.Error())
		return
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var version int
		var createdAt int64

		var promptPreview string

		if err := rows.Scan(&version, &createdAt, &promptPreview); err != nil {
			bot.Send(message.Chat, "Error scanning prompt row: "+err.Error())
			return
		}

		createdTime := time.Unix(createdAt, 0).Format(time.RFC1123)
		versions = append(versions, fmt.Sprintf("Version %d - %s\n%s",
			version, createdTime, promptPreview))
	}

	if len(versions) == 0 {
		bot.Send(message.Chat, fmt.Sprintf("No prompts found for chat %d", chatID))
		return
	}

	response := fmt.Sprintf("Prompt versions for chat %d:\n\n%s",
		chatID, strings.Join(versions, "\n\n"))
	bot.Send(message.Chat, response)
}

func updatePrompt(chatID int64, newPrompt string, bot *registry.BotWrapper, message *telebot.Message) {
	log.Printf("[promptmgr] Updating prompt for chat %d", chatID)
	// Ensure we're updating global prompt if it's a private chat with owner and global keyword is used
	if strings.TrimSpace(newPrompt) == "global" &&
		message.Chat.Type == telebot.ChatPrivate &&
		message.Sender.Username == registry.Config.OwnerUsername {

		// Get the current global prompt
		var prompt string
		var version int
		err := database.DB.QueryRow(`
			SELECT prompt, version FROM prompts 
			WHERE chat_id = ? 
			ORDER BY version DESC LIMIT 1`, GLOBAL_CHAT_ID).Scan(&prompt, &version)

		if err == sql.ErrNoRows {
			bot.Send(message.Chat, "No global prompt is set.")
		} else if err != nil {
			bot.Send(message.Chat, "Error retrieving global prompt: "+err.Error())
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Current global prompt (version %d):\n\n%s", version, prompt))
		}
		return
	}

	// If the prompt is "global <new_prompt>" and user is owner, update global prompt
	if strings.HasPrefix(newPrompt, "global ") &&
		message.Chat.Type == telebot.ChatPrivate &&
		message.Sender.Username == registry.Config.OwnerUsername {

		globalPrompt := strings.TrimPrefix(newPrompt, "global ")
		err := database.RetryWithBackoff(func() error {
			return database.WithTx(context.Background(), func(tx *sql.Tx) error {
				// Get the next version number for global prompt
				var nextVersion int
				err := tx.QueryRow(`
					SELECT COALESCE(MAX(version) + 1, 1) FROM prompts WHERE chat_id = ?`,
					GLOBAL_CHAT_ID).Scan(&nextVersion)
				if err != nil {
					return err
				}

				// Insert new global prompt
				_, err = tx.Exec(`
					INSERT INTO prompts (chat_id, version, prompt, created_at) 
					VALUES (?, ?, ?, ?)`,
					GLOBAL_CHAT_ID, nextVersion, globalPrompt, time.Now().Unix())
				return err
			})
		})

		if err != nil {
			bot.Send(message.Chat, "Error updating global prompt: "+err.Error())
			return
		}

		bot.Send(message.Chat, "Global prompt updated successfully.")
		return
	}

	// Otherwise, update chat-specific prompt
	err := database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			// Get the next version number
			var nextVersion int
			err := tx.QueryRow(`
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
		bot.Send(message.Chat, "Error updating prompt: "+err.Error())
		return
	}

	bot.Send(message.Chat, "Prompt updated successfully.")
}

func revertPrompt(chatID int64, version int, bot *registry.BotWrapper, message *telebot.Message) {
	log.Printf("[promptmgr] Reverting chat %d to version %d", chatID, version)
	// Check if the requested version exists
	var prompt string
	err := database.DB.QueryRow(`
		SELECT prompt FROM prompts 
		WHERE chat_id = ? AND version = ?`,
		chatID, version).Scan(&prompt)

	if err == sql.ErrNoRows {
		bot.Send(message.Chat, fmt.Sprintf("Version %d not found for chat %d", version, chatID))
		return
	} else if err != nil {
		bot.Send(message.Chat, "Error retrieving prompt: "+err.Error())
		return
	}

	// Revert by inserting a new version with the same content
	err = database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			// Get the next version number
			var nextVersion int
			err = tx.QueryRow(`
				SELECT COALESCE(MAX(version) + 1, 1) FROM prompts WHERE chat_id = ?`,
				chatID).Scan(&nextVersion)
			if err != nil {
				return err
			}

			// Insert new prompt based on the old version
			_, err = tx.Exec(`
				INSERT INTO prompts (chat_id, version, prompt, created_at) 
				VALUES (?, ?, ?, ?)`,
				chatID, nextVersion, prompt, time.Now().Unix())
			return err
		})
	})

	if err != nil {
		bot.Send(message.Chat, "Error reverting prompt: "+err.Error())
		return
	}

	bot.Send(message.Chat, fmt.Sprintf("Prompt reverted to version %d content.", version))
}

// GetCurrentPrompt retrieves the current prompt for a chat
// Returns combined global and chat-specific prompts if available, falls back to config
func GetCurrentPrompt(chatID int64, fullPrompt bool) (string, error) {
	log.Printf("[promptmgr] Getting current prompt for chat %d", chatID)

	// Get global prompt if exists
	var globalPrompt string
	err := database.DB.QueryRow(`
		SELECT prompt FROM prompts 
		WHERE chat_id = ? 
		ORDER BY version DESC LIMIT 1`, GLOBAL_CHAT_ID).Scan(&globalPrompt)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("error retrieving global prompt: %v", err)
	}

	// Get chat-specific prompt if exists
	var chatPrompt string
	err = database.DB.QueryRow(`
		SELECT prompt FROM prompts 
		WHERE chat_id = ? 
		ORDER BY version DESC LIMIT 1`, chatID).Scan(&chatPrompt)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("error retrieving chat prompt: %v", err)
	}

	// If no prompts found in DB, use config
	if globalPrompt == "" && chatPrompt == "" {
		log.Printf("[promptmgr] No prompts found in DB, using config")
		globalPrompt = registry.Config.ChatGptSystemPrompt
		// Check for chat-specific config
		for _, chatConfig := range registry.Config.ChatGptConfigPerChat {
			if chatConfig.ChatID == chatID && chatConfig.SystemPrompt != "" {
				chatPrompt = chatConfig.SystemPrompt
				break
			}
		}
	}

	// Combine prompts
	var finalPrompt string
	if globalPrompt != "" && fullPrompt {
		finalPrompt = globalPrompt
	}
	if chatPrompt != "" {
		if finalPrompt != "" {
			finalPrompt += "\n\n"
		}
		finalPrompt += chatPrompt
	}

	return finalPrompt, nil
}
