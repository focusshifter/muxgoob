package promptmgr

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/registry"
	factsutil "github.com/focusshifter/muxgoob/utils/facts"
)

// GLOBAL_CHAT_ID is a special chat ID used for global prompts
const GLOBAL_CHAT_ID int64 = 0

const telegramMessageChunkSize = 4000

// PromptMgrPlugin manages and stores prompts in the database
type PromptMgrPlugin struct{}

func init() {
	registry.RegisterPlugin(&PromptMgrPlugin{})
}

func createPromptTables() {
	// Create the prompts and person facts tables
	_, err := database.DB.Exec(`
		CREATE TABLE IF NOT EXISTS prompts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			version INTEGER NOT NULL,
			prompt TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(chat_id, version)
		);

		CREATE TABLE IF NOT EXISTS person_facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			facts TEXT NOT NULL,
			version INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			UNIQUE(chat_id, user_id, version)
		);

		CREATE INDEX IF NOT EXISTS idx_person_facts_chat_user
			ON person_facts(chat_id, user_id);
	`)
	if err != nil {
		log.Fatal("Failed to create prompts table:", err)
	}
}

func EnsureTables() {
	createPromptTables()
}

func (p *PromptMgrPlugin) Start(interface{}) {
	EnsureTables()
}

func (p *PromptMgrPlugin) Process(message *telebot.Message) {
	if message.Text == "" {
		return
	}

	log.Printf("[promptmgr] Received message: %q", message.Text)

	if strings.HasPrefix(message.Text, "!personfacts") {
		p.processPersonFactsCommand(message)
		return
	}

	if strings.HasPrefix(message.Text, "!personfact") {
		p.processPersonFactCommand(message)
		return
	}

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
		sendPromptMessage(bot, message.Chat, "Sorry, only the bot owner can control prompts.")
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
	sendPromptMessage(bot, message.Chat, "Invalid command format. Use:\n"+
		"!prompt current - Show current chat's prompt\n"+
		"!prompt current <new_prompt> - Set current chat's prompt\n"+
		"!prompt <chat_id> - Show another chat's prompt\n"+
		"!prompt <chat_id> <new_prompt> - Set another chat's prompt")
}

func (p *PromptMgrPlugin) processPersonFactCommand(message *telebot.Message) {
	bot := registry.Bot
	args := strings.Fields(message.Text)
	if len(args) < 3 {
		sendPromptMessage(bot, message.Chat, "Invalid command format. Use:\n"+
			"!personfact <chat_id> <user_id|@username> - Show facts for a chat\n"+
			"!personfact <chat_id> <user_id|@username> <new_facts> - Set facts for another chat")
		return
	}

	isOwner := message.Sender.Username == registry.Config.OwnerUsername
	if !isOwner {
		sendPromptMessage(bot, message.Chat, "Sorry, only the bot owner can control person facts.")
		return
	}

	chatID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("Invalid chat ID: %s", args[1]))
		return
	}
	userArgIndex := 2

	if len(args) <= userArgIndex {
		sendPromptMessage(bot, message.Chat, "Missing user identifier for !personfact command.")
		return
	}

	userID, displayName, err := resolvePersonFactUser(args[userArgIndex])
	if err != nil {
		sendPromptMessage(bot, message.Chat, err.Error())
		return
	}

	if len(args) == userArgIndex+1 {
		showPersonFact(chatID, userID, displayName, bot, message)
		return
	}

	prefixParts := []string{"!personfact"}
	prefixParts = append(prefixParts, args[1])
	prefixParts = append(prefixParts, args[userArgIndex])
	newFacts := strings.TrimSpace(strings.TrimPrefix(message.Text, strings.Join(prefixParts, " ")+" "))
	if newFacts == "" {
		sendPromptMessage(bot, message.Chat, "Facts cannot be empty.")
		return
	}

	updatePersonFact(chatID, userID, displayName, newFacts, bot, message)
}

func (p *PromptMgrPlugin) processPersonFactsCommand(message *telebot.Message) {
	bot := registry.Bot
	args := strings.Fields(message.Text)
	if len(args) != 2 {
		sendPromptMessage(bot, message.Chat, "Invalid command format. Use:\n!personfacts <chat_id>")
		return
	}

	isOwner := message.Sender.Username == registry.Config.OwnerUsername
	if !isOwner {
		sendPromptMessage(bot, message.Chat, "Sorry, only the bot owner can control person facts.")
		return
	}

	chatID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("Invalid chat ID: %s", args[1]))
		return
	}

	showPersonFacts(chatID, bot, message)
}

func resolvePersonFactUser(identifier string) (int64, string, error) {
	if identifier == "" {
		return 0, "", fmt.Errorf("missing user identifier")
	}

	if userID, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		name, lookupErr := lookupUserDisplayName(userID)
		if lookupErr != nil {
			return 0, "", lookupErr
		}
		return userID, name, nil
	}

	username := strings.TrimPrefix(identifier, "@")
	var userID int64
	var storedUsername, firstName, lastName sql.NullString
	err := database.DB.QueryRow(`
		SELECT id, username, first_name, last_name
		FROM users
		WHERE username = ?
		LIMIT 1`, username).Scan(&userID, &storedUsername, &firstName, &lastName)
	if err == sql.ErrNoRows {
		return 0, "", fmt.Errorf("user %q not found", identifier)
	}
	if err != nil {
		return 0, "", fmt.Errorf("error resolving user %q: %v", identifier, err)
	}

	name := strings.TrimSpace(strings.Join([]string{firstName.String, lastName.String}, " "))
	if storedUsername.Valid && storedUsername.String != "" {
		name = storedUsername.String
	}
	if name == "" {
		name = fmt.Sprintf("user_%d", userID)
	}
	return userID, name, nil
}

func lookupUserDisplayName(userID int64) (string, error) {
	var username, firstName, lastName sql.NullString
	err := database.DB.QueryRow(`
		SELECT username, first_name, last_name
		FROM users
		WHERE id = ?`, userID).Scan(&username, &firstName, &lastName)
	if err == sql.ErrNoRows {
		return fmt.Sprintf("user_%d", userID), nil
	}
	if err != nil {
		return "", fmt.Errorf("error looking up user %d: %v", userID, err)
	}

	if username.Valid && username.String != "" {
		return username.String, nil
	}
	name := strings.TrimSpace(strings.Join([]string{firstName.String, lastName.String}, " "))
	if name != "" {
		return name, nil
	}
	return fmt.Sprintf("user_%d", userID), nil
}

func showPersonFact(chatID int64, userID int64, displayName string, bot *registry.BotWrapper, message *telebot.Message) {
	facts, err := GetPersonFacts(chatID, userID)
	if err != nil {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("Error retrieving person facts: %v", err))
		return
	}
	if facts == "" {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("No facts stored for %s in chat %d.", displayName, chatID))
		return
	}
	sendPromptMessage(bot, message.Chat, fmt.Sprintf("Facts for %s in chat %d:\n\n%s", displayName, chatID, facts))
}

func updatePersonFact(chatID int64, userID int64, displayName, facts string, bot *registry.BotWrapper, message *telebot.Message) {
	if err := SavePersonFacts(chatID, userID, facts); err != nil {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("Error updating person facts: %v", err))
		return
	}
	sendPromptMessage(bot, message.Chat, fmt.Sprintf("Facts updated for %s in chat %d.", displayName, chatID))
}

func showPersonFacts(chatID int64, bot *registry.BotWrapper, message *telebot.Message) {
	factMap, err := GetAllPersonFacts(chatID)
	if err != nil {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("Error retrieving person facts: %v", err))
		return
	}
	if len(factMap) == 0 {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("No person facts stored for chat %d.", chatID))
		return
	}

	userIDs := make([]int64, 0, len(factMap))
	for userID := range factMap {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })

	var out strings.Builder
	out.WriteString(fmt.Sprintf("Person facts for chat %d:\n\n", chatID))
	for _, userID := range userIDs {
		displayName, err := lookupUserDisplayName(userID)
		if err != nil {
			displayName = fmt.Sprintf("user_%d", userID)
		}
		out.WriteString(displayName)
		out.WriteString(":\n")
		out.WriteString(strings.TrimSpace(factMap[userID]))
		out.WriteString("\n\n")
	}

	sendPromptMessage(bot, message.Chat, strings.TrimSpace(out.String()))
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
			sendPromptMessage(bot, message.Chat, fmt.Sprintf("%sNo prompt is set for this chat.", chatPrefix))
			return
		} else if err != nil {
			sendPromptMessage(bot, message.Chat, fmt.Sprintf("%sError retrieving prompt: %s", chatPrefix, err.Error()))
			return
		}

		sendPromptMessage(bot, message.Chat, fmt.Sprintf("%sCurrent global prompt (version %d):\n\n%s",
			chatPrefix, version, prompt))
		return
	} else if err != nil {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("%sError retrieving prompt: %s", chatPrefix, err.Error()))
		return
	}

	sendPromptMessage(bot, message.Chat, fmt.Sprintf("%sCurrent chat prompt (version %d):\n\n%s",
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
		sendPromptMessage(bot, message.Chat, "Error retrieving prompt versions: "+err.Error())
		return
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var version int
		var createdAt int64

		var promptPreview string

		if err := rows.Scan(&version, &createdAt, &promptPreview); err != nil {
			sendPromptMessage(bot, message.Chat, "Error scanning prompt row: "+err.Error())
			return
		}

		createdTime := time.Unix(createdAt, 0).Format(time.RFC1123)
		versions = append(versions, fmt.Sprintf("Version %d - %s\n%s",
			version, createdTime, promptPreview))
	}

	if len(versions) == 0 {
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("No prompts found for chat %d", chatID))
		return
	}

	response := fmt.Sprintf("Prompt versions for chat %d:\n\n%s",
		chatID, strings.Join(versions, "\n\n"))
	sendPromptMessage(bot, message.Chat, response)
}

func savePromptVersion(chatID int64, prompt string) error {
	if database.DB == nil {
		return fmt.Errorf("database is not initialized")
	}
	if err := chatmemory.EnsureSchema(database.DB); err != nil {
		return err
	}
	migrateStable := chatID != GLOBAL_CHAT_ID && chatmemory.IsCutover(context.Background(), database.DB, chatID) && chatmemory.HasLegacyStableContext(prompt)
	storedPrompt := prompt
	var stableBodies []string
	if migrateStable {
		stableBodies = chatmemory.ExtractLegacyStableContext(prompt)
		storedPrompt = chatmemory.StripLegacyStableContext(prompt)
	}
	return database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			var nextVersion int
			if err := tx.QueryRow(`SELECT COALESCE(MAX(version) + 1, 1) FROM prompts WHERE chat_id = ?`, chatID).Scan(&nextVersion); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO prompts (chat_id, version, prompt, created_at) VALUES (?, ?, ?, ?)`, chatID, nextVersion, storedPrompt, time.Now().Unix()); err != nil {
				return err
			}
			if migrateStable {
				return chatmemory.NewRepository(database.DB).ReplaceChatLoreTx(context.Background(), tx, chatID, stableBodies, "promptmgr_stable_context")
			}
			return nil
		})
	})
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
			sendPromptMessage(bot, message.Chat, "No global prompt is set.")
		} else if err != nil {
			sendPromptMessage(bot, message.Chat, "Error retrieving global prompt: "+err.Error())
		} else {
			sendPromptMessage(bot, message.Chat, fmt.Sprintf("Current global prompt (version %d):\n\n%s", version, prompt))
		}
		return
	}

	// If the prompt is "global <new_prompt>" and user is owner, update global prompt
	if strings.HasPrefix(newPrompt, "global ") &&
		message.Chat.Type == telebot.ChatPrivate &&
		message.Sender.Username == registry.Config.OwnerUsername {

		globalPrompt := strings.TrimPrefix(newPrompt, "global ")
		err := savePromptVersion(GLOBAL_CHAT_ID, globalPrompt)

		if err != nil {
			sendPromptMessage(bot, message.Chat, "Error updating global prompt: "+err.Error())
			return
		}

		sendPromptMessage(bot, message.Chat, "Global prompt updated successfully.")
		return
	}

	// Otherwise, update chat-specific prompt.
	err := savePromptVersion(chatID, newPrompt)

	if err != nil {
		sendPromptMessage(bot, message.Chat, "Error updating prompt: "+err.Error())
		return
	}

	sendPromptMessage(bot, message.Chat, "Prompt updated successfully.")
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
		sendPromptMessage(bot, message.Chat, fmt.Sprintf("Version %d not found for chat %d", version, chatID))
		return
	} else if err != nil {
		sendPromptMessage(bot, message.Chat, "Error retrieving prompt: "+err.Error())
		return
	}

	// Revert by inserting a new version with the same content. In cutover
	// chats, an explicit legacy Stable context section is moved atomically into
	// typed chat lore rather than revived as a runtime prompt dependency.
	err = savePromptVersion(chatID, prompt)

	if err != nil {
		sendPromptMessage(bot, message.Chat, "Error reverting prompt: "+err.Error())
		return
	}

	sendPromptMessage(bot, message.Chat, fmt.Sprintf("Prompt reverted to version %d content.", version))
}

func sendPromptMessage(bot *registry.BotWrapper, to telebot.Recipient, text string) error {
	for _, chunk := range splitMessage(text, telegramMessageChunkSize) {
		_, err := bot.Send(to, chunk)
		if err != nil {
			log.Printf("[promptmgr] Error sending message: %v", err)
			return err
		}
	}

	return nil
}

func splitMessage(text string, limit int) []string {
	if text == "" {
		return []string{""}
	}

	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}

	var chunks []string
	for len(runes) > limit {
		splitAt := limit
		for i := limit; i > limit-200 && i > 0; i-- {
			if runes[i-1] == '\n' || runes[i-1] == ' ' {
				splitAt = i
				break
			}
		}

		chunks = append(chunks, strings.TrimSpace(string(runes[:splitAt])))
		runes = runes[splitAt:]
		for len(runes) > 0 && (runes[0] == '\n' || runes[0] == ' ') {
			runes = runes[1:]
		}
	}

	if len(runes) > 0 {
		chunks = append(chunks, string(runes))
	}

	return chunks
}

// GetCurrentPrompt retrieves the current prompt for a chat
// Returns combined global and chat-specific prompts if available, falls back to config
func GetCurrentPrompt(chatID int64, fullPrompt bool) (string, error) {
	log.Printf("[promptmgr] Getting current prompt for chat %d", chatID)
	cutover := chatmemory.IsCutover(context.Background(), database.DB, chatID)

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
	if cutover {
		chatPrompt = chatmemory.StripLegacyStableContext(chatPrompt)
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

func GetPersonFacts(chatID int64, userID int64) (string, error) {
	return GetPersonFactsFromDB(database.DB, chatID, userID)
}

func GetPersonFactsFromDB(db *sql.DB, chatID int64, userID int64) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	if chatmemory.IsCutover(context.Background(), db, chatID) {
		results, err := getStructuredPersonFacts(db, chatID, []int64{userID})
		if err != nil {
			return "", err
		}
		return results[userID], nil
	}

	var facts string
	err := db.QueryRow(`
		SELECT facts FROM person_facts
		WHERE chat_id = ? AND user_id = ?
		ORDER BY version DESC
		LIMIT 1`, chatID, userID).Scan(&facts)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("error retrieving person facts: %v", err)
	}
	return factsutil.EnforcePersonFactsBudgets(facts), nil
}

func GetPersonFactsMulti(chatID int64, userIDs []int64) (map[int64]string, error) {
	results := make(map[int64]string)
	if len(userIDs) == 0 {
		return results, nil
	}
	if chatmemory.IsCutover(context.Background(), database.DB, chatID) {
		return getStructuredPersonFacts(database.DB, chatID, userIDs)
	}

	seen := make(map[int64]struct{}, len(userIDs))
	uniqueUserIDs := make([]int64, 0, len(userIDs))
	for _, userID := range userIDs {
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		uniqueUserIDs = append(uniqueUserIDs, userID)
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniqueUserIDs)), ",")
	query := fmt.Sprintf(`
		SELECT pf.user_id, pf.facts
		FROM person_facts pf
		JOIN (
			SELECT user_id, MAX(version) AS max_version
			FROM person_facts
			WHERE chat_id = ? AND user_id IN (%s)
			GROUP BY user_id
		) latest
		ON latest.user_id = pf.user_id AND latest.max_version = pf.version
		WHERE pf.chat_id = ?`, placeholders)

	args := make([]interface{}, 0, len(uniqueUserIDs)+2)
	args = append(args, chatID)
	for _, userID := range uniqueUserIDs {
		args = append(args, userID)
	}
	args = append(args, chatID)

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("error retrieving person facts: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var userID int64
		var facts string
		if err := rows.Scan(&userID, &facts); err != nil {
			return nil, fmt.Errorf("error scanning person facts: %v", err)
		}
		results[userID] = factsutil.EnforcePersonFactsBudgets(facts)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating person facts: %v", err)
	}

	return results, nil
}

func GetAllPersonFacts(chatID int64) (map[int64]string, error) {
	if chatmemory.IsCutover(context.Background(), database.DB, chatID) {
		return getStructuredPersonFacts(database.DB, chatID, nil)
	}

	results := make(map[int64]string)
	rows, err := database.DB.Query(`
		SELECT pf.user_id, pf.facts
		FROM person_facts pf
		JOIN (
			SELECT user_id, MAX(version) AS max_version
			FROM person_facts
			WHERE chat_id = ?
			GROUP BY user_id
		) latest
		ON latest.user_id = pf.user_id AND latest.max_version = pf.version
		WHERE pf.chat_id = ?`, chatID, chatID)
	if err != nil {
		return nil, fmt.Errorf("error retrieving person facts: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var userID int64
		var facts string
		if err := rows.Scan(&userID, &facts); err != nil {
			return nil, fmt.Errorf("error scanning person facts: %v", err)
		}
		results[userID] = factsutil.EnforcePersonFactsBudgets(facts)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating person facts: %v", err)
	}

	return results, nil
}

func SavePersonFacts(chatID int64, userID int64, facts string) error {
	trimmedFacts := factsutil.EnforcePersonFactsBudgets(facts)
	if trimmedFacts == "" {
		return nil
	}
	if !chatmemory.IsCutover(context.Background(), database.DB, chatID) {
		return database.RetryWithBackoff(func() error {
			return database.WithTx(context.Background(), func(tx *sql.Tx) error {
				return SavePersonFactsTx(context.Background(), tx, chatID, userID, trimmedFacts)
			})
		})
	}

	dossier := factsutil.ParseDossier(trimmedFacts)
	bodies := append([]string(nil), dossier.Identity...)
	bodies = append(bodies, dossier.Interests...)
	if len(bodies) == 0 && len(dossier.Appearance) == 0 && trimmedFacts != "" {
		bodies = []string{trimmedFacts}
	}
	return database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			repo := chatmemory.NewRepository(database.DB)
			for _, body := range dossier.Appearance {
				subject := userID
				if _, _, err := repo.AddTx(context.Background(), tx, chatmemory.Entry{ChatID: chatID, Kind: chatmemory.PersonFact, SubjectUserID: &subject, Body: body, Retention: chatmemory.Pinned, SourceType: "stable_appearance"}); err != nil {
					return err
				}
			}
			return repo.ReplacePersonFactsTx(context.Background(), tx, chatID, userID, bodies, "promptmgr_save_person_facts")
		})
	})
}

func getStructuredPersonFacts(db *sql.DB, chatID int64, userIDs []int64) (map[int64]string, error) {
	allowed := make(map[int64]struct{}, len(userIDs))
	for _, userID := range userIDs {
		allowed[userID] = struct{}{}
	}
	entries, err := chatmemory.NewRepository(db).List(context.Background(), chatmemory.Filter{
		ChatID: chatID,
		Kind:   chatmemory.PersonFact,
	})
	if err != nil {
		return nil, fmt.Errorf("error retrieving structured person facts: %v", err)
	}
	grouped := make(map[int64]*factsutil.Dossier)
	for _, entry := range entries {
		if entry.SubjectUserID == nil {
			continue
		}
		userID := *entry.SubjectUserID
		if len(allowed) > 0 {
			if _, ok := allowed[userID]; !ok {
				continue
			}
		}
		body := strings.TrimSpace(entry.Body)
		if body == "" {
			continue
		}
		dossier := grouped[userID]
		if dossier == nil {
			dossier = &factsutil.Dossier{}
			grouped[userID] = dossier
		}
		if entry.Retention == chatmemory.Pinned && !strings.HasPrefix(entry.SourceType, "stable_alias") {
			dossier.Appearance = append(dossier.Appearance, body)
		} else {
			dossier.Identity = append(dossier.Identity, body)
		}
	}
	results := make(map[int64]string, len(grouped))
	for userID, dossier := range grouped {
		results[userID] = factsutil.RenderDossier(factsutil.EnforceDossierBudgets(dossier))
	}
	return results, nil
}

func personFactBodies(raw string) []string {
	trimmed := factsutil.EnforcePersonFactsBudgets(raw)
	dossier := factsutil.ParseDossier(trimmed)
	bodies := append([]string(nil), dossier.Identity...)
	bodies = append(bodies, dossier.Interests...)
	if len(bodies) == 0 && trimmed != "" {
		bodies = []string{trimmed}
	}
	return bodies
}

// SavePersonFactsTx appends one legacy-table version inside a caller-owned
// transaction. Call it only for non-cutover scopes and explicit rollback tools.
func SavePersonFactsTx(ctx context.Context, tx *sql.Tx, chatID int64, userID int64, rawFacts string) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	trimmedFacts := factsutil.EnforcePersonFactsBudgets(rawFacts)
	if trimmedFacts == "" {
		return nil
	}
	var nextVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version) + 1, 1) FROM person_facts WHERE chat_id = ? AND user_id = ?`, chatID, userID).Scan(&nextVersion); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO person_facts (chat_id, user_id, facts, version, created_at) VALUES (?, ?, ?, ?, ?)`, chatID, userID, trimmedFacts, nextVersion, time.Now().Unix())
	return err
}
