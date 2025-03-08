package admin

import (
	"fmt"
	"strings"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

type AdminPlugin struct{}

func init() {
	registry.RegisterPlugin(&AdminPlugin{})
}

func (p *AdminPlugin) Start(config interface{}) {}

func (p *AdminPlugin) Process(message *telebot.Message) {
	// Only process private messages from the owner
	if message.Chat.Type != telebot.ChatPrivate ||
		message.Sender.Username != registry.Config.OwnerUsername {
		return
	}

	// Process AI provider and model commands
	if strings.HasPrefix(message.Text, "!ai ") {
		p.handleAiCommands(message)
		return
	}

	// Check for !list command
	if message.Text == "!list" {
		bot := registry.Bot

		// Query all chats from the database
		rows, err := database.DB.Query(`
			SELECT id, type, title, username, first_name, last_name 
			FROM chats 
			ORDER BY COALESCE(title, username, first_name || ' ' || last_name) ASC
		`)
		if err != nil {
			bot.Send(message.Chat, "Error querying chats: "+err.Error())
			return
		}
		defer rows.Close()

		var chats []string
		for rows.Next() {
			var (
				id                                             int64
				chatType, title, username, firstName, lastName string
			)
			if err := rows.Scan(&id, &chatType, &title, &username, &firstName, &lastName); err != nil {
				bot.Send(message.Chat, "Error scanning chat row: "+err.Error())
				return
			}

			chatName := title
			if chatName == "" {
				if chatType == "private" {
					chatName = username
					if chatName == "" {
						chatName = strings.TrimSpace(fmt.Sprintf("%s %s", firstName, lastName))
					}
				}
			}

			chats = append(chats, fmt.Sprintf("Chat: %s (ID: %d, Type: %s)", chatName, id, chatType))
		}

		if len(chats) == 0 {
			bot.Send(message.Chat, "No chats found in database")
			return
		}

		// Send the list of chats
		response := "List of chats:\n\n" + strings.Join(chats, "\n")
		bot.Send(message.Chat, response)
		return
	}
}

// handleAiCommands processes commands related to AI provider and model settings
func (p *AdminPlugin) handleAiCommands(message *telebot.Message) {
	bot := registry.Bot
	parts := strings.Split(message.Text, " ")

	if len(parts) < 2 {
		bot.Send(message.Chat, "Usage:\n!ai provider [openrouter|openai] [chat_id]\n!ai model <model_name> [chat_id]\n!ai get [chat_id]")
		return
	}

	// Parse chat_id if provided
	var chatID *int64
	if len(parts) >= 4 {
		id, err := fmt.Sscanf(parts[3], "%d", new(int64))
		if err == nil && id > 0 {
			parsedID := int64(id)
			chatID = &parsedID
		}
	}

	switch parts[1] {
	case "provider":
		if len(parts) < 3 {
			bot.Send(message.Chat, "Please specify a provider (openrouter or openai)")
			return
		}

		provider := parts[2]
		if provider != "openrouter" && provider != "openai" {
			bot.Send(message.Chat, "Invalid provider. Use 'openrouter' or 'openai'")
			return
		}

		err := registry.SetAiProvider(chatID, provider)
		if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error setting AI provider: %v", err))
			return
		}

		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("AI provider for chat %d set to: %s", *chatID, provider))
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Global AI provider set to: %s", provider))
		}

	case "model":
		if len(parts) < 3 {
			bot.Send(message.Chat, "Please specify a model name")
			return
		}

		model := parts[2]
		err := registry.SetAiModel(chatID, model)
		if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error setting AI model: %v", err))
			return
		}

		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("AI model for chat %d set to: %s", *chatID, model))
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Global AI model set to: %s", model))
		}

	case "get":
		provider := registry.GetAiProvider(chatID)
		model := registry.GetAiModel(chatID)

		var response string
		if chatID != nil {
			response = fmt.Sprintf("AI settings for chat %d:\nProvider: %s\nModel: %s", *chatID, provider, model)
		} else {
			response = fmt.Sprintf("Global AI settings:\nProvider: %s\nModel: %s", provider, model)
		}

		bot.Send(message.Chat, response)

	default:
		bot.Send(message.Chat, "Unknown AI command. Available commands: provider, model, get")
	}
}
