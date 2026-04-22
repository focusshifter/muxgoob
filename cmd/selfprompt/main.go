package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	chattools "github.com/focusshifter/muxgoob/internal/tools"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	selfpromptplugin "github.com/focusshifter/muxgoob/plugins/selfprompt"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/facts"
)

const (
	bulkDefaultChunkSize = 1000
	bulkLoadPageSize     = 10000
)

var modelOverride string
var historySinceUnix int64
var historySinceLabel string
var debugAI bool

func debugLogAI(stage string, model string, attempt int, raw string) {
	if !debugAI {
		return
	}
	log.Printf("[debug-ai] stage=%s model=%s attempt=%d output:\n%s", stage, model, attempt, strings.TrimSpace(raw))
}

func main() {
	var (
		chatID       int64
		iterations   int
		dbPath       string
		configPath   string
		messageCount int
		waitTime     int
		showHistory  bool
		showPrompt   bool
		dryRun       bool
		bulk         bool
		concurrency  int
		userFilter   string
		sinceDate    string
		debugOutput  bool
		cleanState   bool
	)

	flag.Int64Var(&chatID, "chat", 0, "Chat ID to generate prompts for")
	flag.IntVar(&iterations, "iterations", 0, "Number of prompt generation iterations to run (0 = run until completion)")
	flag.StringVar(&dbPath, "db", "db/muxgoob.sqlite", "Path to SQLite database file")
	flag.StringVar(&configPath, "config", "config.yml", "Path to config.yml file")
	flag.IntVar(&messageCount, "messages", 0, "Number of messages to use for history (0 = use config default)")
	flag.IntVar(&waitTime, "wait", 0, "Seconds to wait between iterations (0 = no wait)")
	flag.BoolVar(&showHistory, "show-history", false, "Show the chat history being used")
	flag.BoolVar(&showPrompt, "show-prompt", true, "Show the generated prompt")
	flag.BoolVar(&dryRun, "dry-run", false, "Don't save the generated prompt to database")
	flag.BoolVar(&bulk, "bulk", false, "Bulk mode: aggregate all messages per user, then generate facts")
	flag.StringVar(&modelOverride, "model", "", "Override AI model for all calls")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of users to process in parallel (bulk mode only)")
	flag.StringVar(&userFilter, "user", "", "Only regenerate facts for one user by @username or numeric user ID; skips prompt regeneration")
	flag.StringVar(&sinceDate, "since-date", "", "Only process messages on or after this date (YYYY-MM-DD or RFC3339)")
	flag.BoolVar(&debugOutput, "debug-ai", false, "Log raw AI outputs for prompt/facts/consolidation")
	flag.BoolVar(&cleanState, "clean-state", false, "Reset chat prompt and/or stored person facts before processing")
	flag.Parse()
	debugAI = debugOutput

	if chatID == 0 {
		log.Fatal("Chat ID is required")
	}
	if concurrency < 1 {
		log.Fatal("Concurrency must be at least 1")
	}

	// Initialize registry and load config
	registry.LoadConfig(configPath)

	// Initialize database
	var err error
	database.DB, err = sql.Open("sqlite3", dbPath+"?_journal=WAL&_busy_timeout=10000&_synchronous=NORMAL&cache=shared")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.DB.Close()

	// Initialize registry database settings
	registry.InitializeDbSettings()
	promptmgr.EnsureTables()
	if strings.TrimSpace(sinceDate) != "" {
		sinceTime, err := parseSinceDate(sinceDate)
		if err != nil {
			log.Fatalf("invalid since-date: %v", err)
		}
		historySinceUnix = sinceTime.Unix()
		historySinceLabel = sinceTime.Format(time.RFC3339)
		fmt.Printf("Limiting history to messages since %s\n", historySinceLabel)
	}

	var selectedUser *activeChatUser
	if strings.TrimSpace(userFilter) != "" {
		resolvedUser, err := resolveRequestedUser(userFilter)
		if err != nil {
			log.Fatal(err)
		}
		selectedUser = &resolvedUser
		fmt.Printf("Limiting run to user %s (%d); prompt regeneration disabled\n", selectedUser.Name, selectedUser.ID)
	}
	if cleanState {
		if err := resetStoredState(chatID, selectedUser); err != nil {
			log.Fatalf("failed to reset stored state: %v", err)
		}
	}

	// Set message count from config if not specified
	if messageCount == 0 {
		if bulk {
			messageCount = bulkDefaultChunkSize
		} else {
			messageCount = registry.Config.ChatGptHistoryDepth
		}
	}

	fmt.Printf("Starting prompt generation for chat %d with %d iterations\n", chatID, iterations)
	fmt.Printf("Using %d messages for history\n", messageCount)

	// Get total message count for the chat or selected user
	totalMessages := getScopedMessageCount(chatID, selectedUser)
	if totalMessages == 0 {
		if selectedUser != nil {
			log.Fatalf("No messages found for user %s (%d) in chat %d", selectedUser.Name, selectedUser.ID, chatID)
		}
		log.Fatalf("No messages found for chat %d", chatID)
	}

	if selectedUser != nil {
		fmt.Printf("Found a total of %d messages for %s in chat history\n", totalMessages, selectedUser.Name)
	} else {
		fmt.Printf("Found a total of %d messages in chat history\n", totalMessages)
	}
	if debugAI {
		fmt.Printf("Debug AI logging enabled\n")
	}

	if bulk {
		fmt.Printf("Running in bulk mode with chunk size %d and concurrency %d\n", messageCount, concurrency)
		if modelOverride != "" {
			fmt.Printf("Using model override: %s\n", modelOverride)
		}
		runBulkMode(chatID, totalMessages, messageCount, concurrency, showHistory, showPrompt, dryRun, selectedUser)
		return
	}

	// Calculate how many batches we'll process
	batchCount := (totalMessages + messageCount - 1) / messageCount // Ceiling division
	if iterations > 0 && iterations < batchCount {
		batchCount = iterations
	}

	fmt.Printf("Will process %d batches with %d messages per batch\n", batchCount, messageCount)

	// Process messages in batches
	for i := 1; i <= batchCount; i++ {
		fmt.Printf("\n--- Batch %d/%d ---\n", i, batchCount)

		// Get the current prompt
		currentPrompt, err := promptmgr.GetCurrentPrompt(chatID, false)
		if err != nil {
			log.Printf("Error getting current prompt: %v", err)
			currentPrompt = ""
		}

		// Calculate offset for this batch
		// We want to process from oldest to newest, so we start from 0 and increment by messageCount
		offset := (i - 1) * messageCount
		if offset < 0 {
			offset = 0
		}

		// Get chat history for this batch
		messages := retrieveScopedHistoryBatch(chatID, selectedUser, messageCount, offset)
		if len(messages) == 0 {
			log.Printf("No messages found for batch %d", i)
			continue
		}

		fmt.Printf("Processing %d messages (offset: %d)\n", len(messages), offset)

		// Format history for display
		history := generateChatGptHistory(messages)

		if showHistory {
			fmt.Println("\n=== Chat History ===")
			fmt.Println(history)
			fmt.Print("=== End of History ===\n\n")
		}

		// Generate new prompt
		var newPrompt string
		if !dryRun {
			fmt.Println("Updating person facts...")
			updatePersonFacts(chatID, messages, history, selectedUser)

			if selectedUser == nil {
				fmt.Println("Generating new prompt...")
				newPrompt = generateNewPrompt(chatID, history, currentPrompt)
			}

			if selectedUser == nil && showPrompt {
				fmt.Println("\n=== Generated Prompt ===")
				fmt.Println(newPrompt)
				fmt.Print("=== End of Prompt ===\n\n")
			}
		}

		// Save the prompt if not in dry-run mode
		if selectedUser != nil {
			fmt.Println("Single-user mode - prompt regeneration skipped")
		} else if !dryRun {
			err = savePrompt(chatID, newPrompt)
			if err != nil {
				log.Printf("Error saving prompt: %v", err)
			} else {
				fmt.Println("Prompt saved to database")
			}
		} else {
			fmt.Println("Dry run - prompt not saved to database")
		}

		// Wait between iterations if specified
		if waitTime > 0 && i < batchCount {
			fmt.Printf("Waiting %d seconds before next batch...\n", waitTime)
			time.Sleep(time.Duration(waitTime) * time.Second)
		}
	}

	fmt.Println("\nPrompt generation complete")
}

// getTotalMessageCount returns the total number of messages in a chat
func getTotalMessageCount(chatID int64) int {
	var count int
	var err error
	if historySinceUnix > 0 {
		err = database.DB.QueryRow(
			`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND unixtime >= ?`,
			chatID, historySinceUnix).Scan(&count)
	} else {
		err = database.DB.QueryRow(
			`SELECT COUNT(*) FROM messages WHERE chat_id = ?`,
			chatID).Scan(&count)
	}
	if err != nil {
		log.Printf("Error counting messages: %v", err)
		return 0
	}
	return count
}

func getUserMessageCount(chatID int64, userID int64) int {
	var count int
	var err error
	if historySinceUnix > 0 {
		err = database.DB.QueryRow(
			`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND sender_id = ? AND unixtime >= ?`,
			chatID, userID, historySinceUnix).Scan(&count)
	} else {
		err = database.DB.QueryRow(
			`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND sender_id = ?`,
			chatID, userID).Scan(&count)
	}
	if err != nil {
		log.Printf("Error counting messages for user %d: %v", userID, err)
		return 0
	}
	return count
}

func getScopedMessageCount(chatID int64, selectedUser *activeChatUser) int {
	if selectedUser != nil {
		return getUserMessageCount(chatID, selectedUser.ID)
	}
	return getTotalMessageCount(chatID)
}

func parseSinceDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}

	layouts := []string{time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		if layout == "2006-01-02" {
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.Local), nil
		}
		return parsed, nil
	}

	return time.Time{}, fmt.Errorf("expected YYYY-MM-DD or RFC3339")
}

func resetStoredState(chatID int64, selectedUser *activeChatUser) error {
	if selectedUser != nil {
		fmt.Printf("Resetting stored facts for %s (%d) in chat %d\n", selectedUser.Name, selectedUser.ID, chatID)
		return insertEmptyPersonFactsVersion(chatID, selectedUser.ID)
	}

	fmt.Printf("Resetting chat prompt and all stored person facts for chat %d\n", chatID)
	if err := savePrompt(chatID, ""); err != nil {
		return err
	}

	rows, err := database.DB.Query(`SELECT DISTINCT user_id FROM person_facts WHERE chat_id = ?`, chatID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var resetCount int
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return err
		}
		if err := insertEmptyPersonFactsVersion(chatID, userID); err != nil {
			return err
		}
		resetCount++
	}
	if err := rows.Err(); err != nil {
		return err
	}

	fmt.Printf("Reset prompt and %d stored fact profiles\n", resetCount)
	return nil
}

func insertEmptyPersonFactsVersion(chatID int64, userID int64) error {
	return database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			var nextVersion int
			err := tx.QueryRow(`
				SELECT COALESCE(MAX(version) + 1, 1)
				FROM person_facts
				WHERE chat_id = ? AND user_id = ?`, chatID, userID).Scan(&nextVersion)
			if err != nil {
				return err
			}

			_, err = tx.Exec(`
				INSERT INTO person_facts (chat_id, user_id, facts, version, created_at)
				VALUES (?, ?, '', ?, ?)`,
				chatID, userID, nextVersion, time.Now().Unix())
			return err
		})
	})
}

// retrieveHistoryBatch gets a batch of chat history from the database
// offset is the number of messages to skip from the beginning
func retrieveHistoryBatch(chatID int64, limit int, offset int) []telebot.Message {
	var (
		rows *sql.Rows
		err  error
	)
	if historySinceUnix > 0 {
		rows, err = database.DB.Query(
			`SELECT data FROM messages 
			WHERE chat_id = ? AND unixtime >= ?
			ORDER BY unixtime ASC LIMIT ? OFFSET ?`,
			chatID, historySinceUnix, limit, offset)
	} else {
		rows, err = database.DB.Query(
			`SELECT data FROM messages 
			WHERE chat_id = ? 
			ORDER BY unixtime ASC LIMIT ? OFFSET ?`,
			chatID, limit, offset)
	}
	if err != nil {
		log.Printf("Error retrieving chat history batch: %v", err)
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

	log.Printf("Retrieved %v messages from batch", len(messages))

	return messages
}

func retrieveHistoryBatchForUser(chatID int64, userID int64, limit int, offset int) []telebot.Message {
	var (
		rows *sql.Rows
		err  error
	)
	if historySinceUnix > 0 {
		rows, err = database.DB.Query(
			`SELECT data FROM messages
			WHERE chat_id = ? AND sender_id = ? AND unixtime >= ?
			ORDER BY unixtime ASC LIMIT ? OFFSET ?`,
			chatID, userID, historySinceUnix, limit, offset)
	} else {
		rows, err = database.DB.Query(
			`SELECT data FROM messages
			WHERE chat_id = ? AND sender_id = ?
			ORDER BY unixtime ASC LIMIT ? OFFSET ?`,
			chatID, userID, limit, offset)
	}
	if err != nil {
		log.Printf("Error retrieving chat history batch for user %d: %v", userID, err)
		return nil
	}
	defer rows.Close()

	var messages []telebot.Message
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			log.Printf("Error scanning user message: %v", err)
			continue
		}

		var msg telebot.Message
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			log.Printf("Error unmarshaling user message: %v", err)
			continue
		}
		messages = append(messages, msg)
	}

	log.Printf("Retrieved %v messages from batch for user %d", len(messages), userID)
	return messages
}

func retrieveScopedHistoryBatch(chatID int64, selectedUser *activeChatUser, limit int, offset int) []telebot.Message {
	if selectedUser != nil {
		return retrieveHistoryBatchForUser(chatID, selectedUser.ID, limit, offset)
	}
	return retrieveHistoryBatch(chatID, limit, offset)
}

// retrieveHistoryForChat gets the chat history from the database
// This is kept for backward compatibility
func retrieveHistoryForChat(chatID int64, messageCount int) []telebot.Message {
	rows, err := database.DB.Query(
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
		parentMessages := retrieveMessagesByIDs(database.DB, chatID, replyParentIDs)
		messages = append(messages, parentMessages...)
	}

	// Sort by timestamp for consistent order
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

type activeChatUser struct {
	ID   int64
	Name string
}

type bulkUserResult struct {
	User         activeChatUser
	MessageCount int
	ChunkCount   int
	DoneChunks   int
	Err          error
}

func loadAllMessages(chatID int64, totalMessages int) []telebot.Message {
	if totalMessages <= 0 {
		return nil
	}

	messages := make([]telebot.Message, 0, totalMessages)
	for offset := 0; offset < totalMessages; offset += bulkLoadPageSize {
		batch := retrieveHistoryBatch(chatID, bulkLoadPageSize, offset)
		if len(batch) == 0 {
			break
		}
		messages = append(messages, batch...)
		fmt.Printf("Loaded %d/%d messages\n", len(messages), totalMessages)
		if len(batch) < bulkLoadPageSize {
			break
		}
	}

	return messages
}

func loadAllMessagesForUser(chatID int64, user activeChatUser, totalMessages int) []telebot.Message {
	if totalMessages <= 0 {
		return nil
	}

	messages := make([]telebot.Message, 0, totalMessages)
	for offset := 0; offset < totalMessages; offset += bulkLoadPageSize {
		batch := retrieveHistoryBatchForUser(chatID, user.ID, bulkLoadPageSize, offset)
		if len(batch) == 0 {
			break
		}
		messages = append(messages, batch...)
		fmt.Printf("Loaded %d/%d messages for %s\n", len(messages), totalMessages, user.Name)
		if len(batch) < bulkLoadPageSize {
			break
		}
	}

	return messages
}

func chunkCount(total int, chunkSize int) int {
	if total == 0 || chunkSize <= 0 {
		return 0
	}
	return (total + chunkSize - 1) / chunkSize
}

func loadBotUserIDs(chatID int64) map[int64]struct{} {
	botUserIDs := make(map[int64]struct{})

	rows, err := database.DB.Query(
		`SELECT DISTINCT m.sender_id
		FROM messages m
		JOIN users u ON u.id = m.sender_id
		WHERE m.chat_id = ?
		  AND m.sender_id IS NOT NULL
		  AND (
			json_extract(m.data, '$.from.is_bot') = 1
			OR json_extract(u.data, '$.is_bot') = 1
			OR LOWER(COALESCE(u.username, '')) LIKE '%bot'
		  )`,
		chatID,
	)
	if err != nil {
		rows, err = database.DB.Query(
			`SELECT DISTINCT m.sender_id
			FROM messages m
			JOIN users u ON u.id = m.sender_id
			WHERE m.chat_id = ?
			  AND m.sender_id IS NOT NULL
			  AND (
				m.data LIKE '%"is_bot":true%'
				OR u.data LIKE '%"is_bot":true%'
				OR LOWER(COALESCE(u.username, '')) LIKE '%bot'
			  )`,
			chatID,
		)
		if err != nil {
			log.Printf("Error loading bot user IDs for chat %d: %v", chatID, err)
			return botUserIDs
		}
	}
	defer rows.Close()

	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			log.Printf("Error scanning bot user ID for chat %d: %v", chatID, err)
			continue
		}
		botUserIDs[userID] = struct{}{}
	}

	return botUserIDs
}

func collectBulkUsers(messages []telebot.Message, botUserIDs map[int64]struct{}) ([]activeChatUser, int) {
	seenUsers := make(map[int64]struct{})
	seenBots := make(map[int64]struct{})
	users := make([]activeChatUser, 0)

	for _, message := range messages {
		if message.Sender == nil || message.Sender.ID == 0 {
			continue
		}

		userID := int64(message.Sender.ID)
		if _, ok := botUserIDs[userID]; ok {
			seenBots[userID] = struct{}{}
			continue
		}
		if _, ok := seenUsers[userID]; ok {
			continue
		}
		seenUsers[userID] = struct{}{}
		users = append(users, activeChatUser{ID: userID, Name: displayName(message.Sender)})
	}

	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users, len(seenBots)
}

func processBulkUser(chatID int64, user activeChatUser, messages []telebot.Message, chunkSize int, dryRun bool) bulkUserResult {
	userMessages := filterMessagesByUser(messages, user.ID)
	result := bulkUserResult{
		User:         user,
		MessageCount: len(userMessages),
		ChunkCount:   chunkCount(len(userMessages), chunkSize),
	}

	if len(userMessages) == 0 {
		return result
	}

	currentFacts := ""
	for start, chunkIndex := 0, 1; start < len(userMessages); start, chunkIndex = start+chunkSize, chunkIndex+1 {
		end := start + chunkSize
		if end > len(userMessages) {
			end = len(userMessages)
		}

		fmt.Printf("[%s] chunk %d/%d start (%d messages)\n", user.Name, chunkIndex, result.ChunkCount, end-start)

		newFacts, err := generateUserFacts(
			chatID,
			user,
			generateChatGptHistory(userMessages[start:end]),
			currentFacts,
		)
		if err != nil {
			result.DoneChunks = chunkIndex - 1
			result.Err = fmt.Errorf("chunk %d/%d: %w", chunkIndex, result.ChunkCount, err)
			return result
		}

		newFacts = strings.TrimSpace(newFacts)
		if newFacts == strings.TrimSpace(currentFacts) {
			fmt.Printf("[%s] chunk %d/%d done (unchanged)\n", user.Name, chunkIndex, result.ChunkCount)
		} else {
			fmt.Printf("[%s] chunk %d/%d done (updated)\n", user.Name, chunkIndex, result.ChunkCount)
		}

		currentFacts = newFacts
		result.DoneChunks = chunkIndex
	}

	if dryRun || currentFacts == "" {
		return result
	}

	if err := promptmgr.SavePersonFacts(chatID, user.ID, currentFacts); err != nil {
		result.Err = err
	}

	return result
}

func runBulkMode(chatID int64, totalMessages int, chunkSize int, concurrency int, showHistory bool, showPrompt bool, dryRun bool, selectedUser *activeChatUser) {
	startedAt := time.Now()

	var allMessages []telebot.Message
	if selectedUser != nil {
		fmt.Printf("Loading messages for %s in chat %d...\n", selectedUser.Name, chatID)
		allMessages = loadAllMessagesForUser(chatID, *selectedUser, totalMessages)
	} else {
		fmt.Printf("Loading messages for chat %d...\n", chatID)
		allMessages = loadAllMessages(chatID, totalMessages)
	}
	if len(allMessages) == 0 {
		if selectedUser != nil {
			log.Fatalf("No messages loaded for user %s in chat %d", selectedUser.Name, chatID)
		}
		log.Fatalf("No messages loaded for chat %d", chatID)
	}

	if selectedUser != nil {
		fmt.Printf("\n=== Phase 1: Person Facts (single user) ===\n")
		result := processBulkUser(chatID, *selectedUser, allMessages, chunkSize, dryRun)
		if result.Err != nil {
			log.Fatalf("%s failed (%d msgs, %d/%d chunks): %v", result.User.Name, result.MessageCount, result.DoneChunks, result.ChunkCount, result.Err)
		}
		fmt.Printf("%s done (%d msgs, %d/%d chunks)\n", result.User.Name, result.MessageCount, result.DoneChunks, result.ChunkCount)
		fmt.Printf("Phase 1 complete in %s\n", time.Since(startedAt).Round(time.Second))
		fmt.Println("Single-user mode - prompt regeneration skipped")
		fmt.Printf("Done. Total time: %s\n", time.Since(startedAt).Round(time.Second))
		return
	}

	botUserIDs := loadBotUserIDs(chatID)
	users, skippedBots := collectBulkUsers(allMessages, botUserIDs)
	phase1StartedAt := time.Now()
	fmt.Printf("\n=== Phase 1: Person Facts (%d users, %d concurrent) ===\n", len(users), concurrency)
	if skippedBots > 0 {
		fmt.Printf("Skipping %d bot accounts during person fact extraction\n", skippedBots)
	}

	jobs := make(chan activeChatUser)
	results := make(chan bulkUserResult, len(users))
	var workers sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for user := range jobs {
				results <- processBulkUser(chatID, user, allMessages, chunkSize, dryRun)
			}
		}()
	}

	go func() {
		for _, user := range users {
			jobs <- user
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	completedUsers := 0
	for result := range results {
		completedUsers++
		if result.Err != nil {
			fmt.Printf("[%d/%d] %s failed (%d msgs, %d/%d chunks): %v\n", completedUsers, len(users), result.User.Name, result.MessageCount, result.DoneChunks, result.ChunkCount, result.Err)
			continue
		}
		fmt.Printf("[%d/%d] %s done (%d msgs, %d/%d chunks)\n", completedUsers, len(users), result.User.Name, result.MessageCount, result.DoneChunks, result.ChunkCount)
	}

	fmt.Printf("Phase 1 complete in %s\n", time.Since(phase1StartedAt).Round(time.Second))

	phase2StartedAt := time.Now()
	totalPromptChunks := chunkCount(len(allMessages), chunkSize)
	fmt.Printf("\n=== Phase 2: Chat Prompt (%d chunks) ===\n", totalPromptChunks)

	currentPrompt := ""
	for start, chunkIndex := 0, 1; start < len(allMessages); start, chunkIndex = start+chunkSize, chunkIndex+1 {
		end := start + chunkSize
		if end > len(allMessages) {
			end = len(allMessages)
		}

		history := generateChatGptHistory(allMessages[start:end])
		if showHistory {
			fmt.Printf("\n=== Chat History Chunk %d/%d ===\n", chunkIndex, totalPromptChunks)
			fmt.Println(history)
			fmt.Print("=== End of History Chunk ===\n\n")
		}

		currentPrompt = generateNewPrompt(chatID, history, currentPrompt)
		fmt.Printf("Prompt chunk %d/%d done\n", chunkIndex, totalPromptChunks)
	}

	if showPrompt {
		fmt.Println("\n=== Generated Prompt ===")
		fmt.Println(currentPrompt)
		fmt.Print("=== End of Prompt ===\n\n")
	}

	if dryRun {
		fmt.Println("Dry run - prompt not saved to database")
	} else {
		if err := savePrompt(chatID, currentPrompt); err != nil {
			log.Printf("Error saving prompt: %v", err)
		} else {
			fmt.Println("Prompt saved to database")
		}
	}

	fmt.Printf("Phase 2 complete in %s\n", time.Since(phase2StartedAt).Round(time.Second))
	fmt.Printf("Done. Total time: %s\n", time.Since(startedAt).Round(time.Second))
}

// generateChatGptHistory formats the messages for the AI prompt
func generateChatGptHistory(messages []telebot.Message) string {
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
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users
}

func resolveRequestedUser(identifier string) (activeChatUser, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return activeChatUser{}, fmt.Errorf("user identifier cannot be empty")
	}

	if strings.HasPrefix(identifier, "@") {
		identifier = strings.TrimPrefix(identifier, "@")
	}

	if userID, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		return lookupUserByID(userID)
	}

	return lookupUserByUsername(identifier)
}

func lookupUserByID(userID int64) (activeChatUser, error) {
	row := database.DB.QueryRow(`
		SELECT id, COALESCE(NULLIF(username, ''), TRIM(COALESCE(first_name, '') || ' ' || COALESCE(last_name, '')), 'user_' || id)
		FROM users
		WHERE id = ?
	`, userID)

	var user activeChatUser
	if err := row.Scan(&user.ID, &user.Name); err != nil {
		if err == sql.ErrNoRows {
			return activeChatUser{}, fmt.Errorf("user %d not found", userID)
		}
		return activeChatUser{}, err
	}

	return user, nil
}

func lookupUserByUsername(username string) (activeChatUser, error) {
	row := database.DB.QueryRow(`
		SELECT id, COALESCE(NULLIF(username, ''), TRIM(COALESCE(first_name, '') || ' ' || COALESCE(last_name, '')), 'user_' || id)
		FROM users
		WHERE LOWER(username) = LOWER(?)
	`, username)

	var user activeChatUser
	if err := row.Scan(&user.ID, &user.Name); err != nil {
		if err == sql.ErrNoRows {
			return activeChatUser{}, fmt.Errorf("user @%s not found", username)
		}
		return activeChatUser{}, err
	}

	return user, nil
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
			model = selfpromptplugin.GetModel(chatID)
		}
		if model == "" {
			model = registry.GetAiModel(chatID)
		}
		if modelOverride != "" {
			model = modelOverride
		}
		return openai.NewClientWithConfig(config), model
	case "openai-codex":
		if model == "" {
			model = selfpromptplugin.GetModel(chatID)
		}
		if model == "" {
			model = strings.TrimSpace(registry.GetAiModel(chatID))
		}
		if model == "" {
			model = "gpt-5.4"
		}
		modelInfo := openaicodex.NormalizeConfiguredModel(model)
		if modelOverride != "" {
			modelInfo = openaicodex.NormalizeConfiguredModel(modelOverride)
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
			model = selfpromptplugin.GetModel(chatID)
		}
		if model == "" {
			model = "gpt-4o-mini"
		}
		if modelOverride != "" {
			model = modelOverride
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
	return len(trimmed) >= selfpromptplugin.DefaultCompactChars() || bulletCount >= selfpromptplugin.DefaultCompactBullets()
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
			log.Printf("Error consolidating facts for user %s: %v", userName, err)
			return currentFacts
		}
		if len(resp.Choices) == 0 {
			return currentFacts
		}
		debugLogAI(fmt.Sprintf("facts-consolidation user=%s", userName), model, attempt, resp.Choices[0].Message.Content)

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

func bootstrapFactsRequired(currentFacts string) bool {
	return strings.TrimSpace(currentFacts) == ""
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
		debugLogAI("chat-prompt-consolidation", model, attempt, resp.Choices[0].Message.Content)
		candidate := facts.EnforceChatPromptBudgets(facts.ParseChatPrompt(resp.Choices[0].Message.Content))
		value := facts.RenderChatPrompt(candidate)
		if strings.TrimSpace(value) == "" {
			return currentPrompt
		}
		return value
	}
	return currentPrompt
}

func updatePersonFacts(chatID int64, messages []telebot.Message, _ string, selectedUser *activeChatUser) {
	users := collectActiveUsers(messages)
	if selectedUser != nil {
		users = []activeChatUser{*selectedUser}
	}
	for _, user := range users {
		userMessages := filterMessagesByUser(messages, user.ID)
		if len(userMessages) == 0 {
			continue
		}
		userHistory := generateChatGptHistory(userMessages)

		currentFacts, err := promptmgr.GetPersonFacts(chatID, user.ID)
		if err != nil {
			log.Printf("Error getting facts for user %d: %v", user.ID, err)
			continue
		}

		newFacts, err := generateUserFacts(chatID, user, userHistory, currentFacts)
		if err != nil {
			log.Printf("Error generating facts for user %d: %v", user.ID, err)
			continue
		}
		newFacts = strings.TrimSpace(newFacts)
		if newFacts == "" || newFacts == strings.TrimSpace(currentFacts) {
			continue
		}

		if err := promptmgr.SavePersonFacts(chatID, user.ID, newFacts); err != nil {
			log.Printf("Error saving facts for user %d: %v", user.ID, err)
		}
	}
}

// generateNewPrompt generates a new prompt based on chat history
func generateNewPrompt(chatID int64, history string, currentPrompt string) string {
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
			log.Printf("Error generating new prompt: %v", err)
			return currentPrompt
		}
		if len(resp.Choices) == 0 {
			log.Printf("No prompt generated by GPT")
			return currentPrompt
		}

		raw := strings.TrimSpace(resp.Choices[0].Message.Content)
		debugLogAI("chat-prompt-delta", model, attempt, raw)
		if raw == "" {
			log.Printf("Empty prompt generated by GPT")
			return currentPrompt
		}
		if facts.IsNoChanges(raw) {
			return currentPrompt
		}

		delta, accepted, retryable, reason := facts.EvaluateChatDelta(raw)
		if !accepted {
			if retryable && attempt == 1 {
				log.Printf("Retrying prompt generation after invalid output: %s", reason)
				continue
			}
			log.Printf("Skipping prompt update after invalid output: %s", reason)
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

		log.Printf("Generated new prompt: %s", newPrompt)
		return newPrompt
	}

	return currentPrompt
}

func generateUserFacts(chatID int64, user activeChatUser, history string, currentFacts string) (string, error) {
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
			return currentFacts, err
		}
		if len(resp.Choices) == 0 {
			return currentFacts, fmt.Errorf("no facts generated for user %d", user.ID)
		}

		raw := strings.TrimSpace(resp.Choices[0].Message.Content)
		debugLogAI(fmt.Sprintf("person-facts-delta user=%s(%d)", user.Name, user.ID), model, attempt, raw)
		if facts.IsNoChanges(raw) {
			if requireBootstrap && attempt == 1 {
				log.Printf("Retrying facts generation for user %d because profile is empty and model returned NO_CHANGES", user.ID)
				continue
			}
			return currentFacts, nil
		}

		delta, accepted, retryable, reason := facts.EvaluateDelta(raw)
		if accepted {
			current := facts.ParseDossier(currentFacts)
			delta = facts.SanitizeDeltaForPerson(delta, user.Name)
			delta = facts.FilterDeltaForDossier(current, delta)
			if delta == nil || len(delta.Identity)+len(delta.Interests) == 0 {
				return currentFacts, nil
			}
			merged := facts.ApplyDelta(current, delta)
			candidate := facts.RenderDossier(merged)
			candidate = consolidatePersonFacts(chatID, user.Name, candidate)
			evaluation := facts.EvaluatePersonFacts(currentFacts, candidate)
			if evaluation.Accepted {
				return evaluation.Value, nil
			}
			log.Printf("Skipping facts update for user %d after merge failed safety check: %s", user.ID, evaluation.Reason)
			return currentFacts, nil
		}
		if retryable && attempt == 1 {
			log.Printf("Retrying facts generation for user %d after invalid output: %s", user.ID, reason)
			continue
		}

		log.Printf("Skipping facts update for user %d after invalid output: %s", user.ID, reason)
		return currentFacts, nil
	}

	return currentFacts, nil
}

// savePrompt saves a new prompt to the database
func savePrompt(chatID int64, prompt string) error {
	return database.RetryWithBackoff(func() error {
		return database.WithTx(context.Background(), func(tx *sql.Tx) error {
			// Get next version
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
				chatID, nextVersion, prompt, time.Now().Unix())
			return err
		})
	})
}
