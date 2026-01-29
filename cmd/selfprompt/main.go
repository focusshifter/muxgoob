package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/plugins/promptmgr"
	"github.com/focusshifter/muxgoob/registry"
)

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
	flag.Parse()

	if chatID == 0 {
		log.Fatal("Chat ID is required")
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

	// Set message count from config if not specified
	if messageCount == 0 {
		messageCount = registry.Config.ChatGptHistoryDepth
	}

	fmt.Printf("Starting prompt generation for chat %d with %d iterations\n", chatID, iterations)
	fmt.Printf("Using %d messages for history\n", messageCount)

	// Get total message count for the chat
	totalMessages := getTotalMessageCount(chatID)
	if totalMessages == 0 {
		log.Fatalf("No messages found for chat %d", chatID)
	}

	fmt.Printf("Found a total of %d messages in chat history\n", totalMessages)

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
		messages := retrieveHistoryBatch(chatID, messageCount, offset)
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
			fmt.Println("Generating new prompt...")
			newPrompt = generateNewPrompt(history, currentPrompt)

			if showPrompt {
				fmt.Println("\n=== Generated Prompt ===")
				fmt.Println(newPrompt)
				fmt.Print("=== End of Prompt ===\n\n")
			}
		}

		// Save the prompt if not in dry-run mode
		if !dryRun {
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
	err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE chat_id = ?`,
		chatID).Scan(&count)
	if err != nil {
		log.Printf("Error counting messages: %v", err)
		return 0
	}
	return count
}

// retrieveHistoryBatch gets a batch of chat history from the database
// offset is the number of messages to skip from the beginning
func retrieveHistoryBatch(chatID int64, limit int, offset int) []telebot.Message {
	rows, err := database.DB.Query(
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

// generateChatGptHistory formats the messages for the AI prompt
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

// generateNewPrompt generates a new prompt based on chat history
func generateNewPrompt(history string, currentPrompt string) string {
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

Output only the new bot system prompt text. Do not include meta-instructions, role labels, or phrases like "You are a prompt engineer".
Use the main language of the chat, such as English or Russian.

**Discussion Topics:**  
- [List key topics from chat history, e.g., "Programming languages", "Speed and performance"]  

**[USERNAME]:**  
- [Traits, relationships, interests, e.g., "Analytical, debates USER456, prefers Go"]  
**[USERNAME]:**  
- [Traits, relationships, interests, e.g., "Practical, counters USER123, likes Python"]

`

	userMsg += `**Current Prompt to Refine:** ` + currentPrompt + `

`
	userMsg += `**Chat History:**
` + history

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
		log.Printf("Error generating new prompt: %v", err)
		return currentPrompt
	}

	if len(resp.Choices) == 0 {
		log.Printf("No prompt generated by GPT")
		return currentPrompt
	}

	newPrompt := resp.Choices[0].Message.Content
	if newPrompt == "" {
		log.Printf("Empty prompt generated by GPT")
		return currentPrompt
	}

	log.Printf("Generated new prompt: %s", newPrompt)

	return newPrompt
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
