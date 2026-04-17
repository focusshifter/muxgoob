package admin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	selfpromptplugin "github.com/focusshifter/muxgoob/plugins/selfprompt"
	"github.com/focusshifter/muxgoob/plugins/spotify"
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

	// Process Spotify commands
	if strings.HasPrefix(message.Text, "!spotify ") {
		p.handleSpotifyCommands(message)
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
		bot.Send(message.Chat, "Usage:\n!ai provider [openrouter|openai] [chat_id]\n!ai model <model_name> [chat_id]\n!ai model image <model_name> [chat_id]\n!ai model selfprompt <model_name> [chat_id]\n!ai get [chat_id]")
		return
	}

	switch parts[1] {
	case "provider":
		var chatID *int64
		if len(parts) >= 4 {
			parsedID, err := strconv.ParseInt(parts[3], 10, 64)
			if err == nil {
				chatID = &parsedID
			}
		}
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
		if parts[2] == "selfprompt" {
			if len(parts) < 4 {
				bot.Send(message.Chat, "Please specify a selfprompt model name")
				return
			}
			var chatID *int64
			if len(parts) >= 5 {
				parsedID, err := strconv.ParseInt(parts[4], 10, 64)
				if err == nil {
					chatID = &parsedID
				}
			}
			model := parts[3]
			err := selfpromptplugin.SetModel(chatID, model)
			if err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error setting selfprompt model: %v", err))
				return
			}
			if chatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Selfprompt model for chat %d set to: %s", *chatID, model))
			} else {
				bot.Send(message.Chat, fmt.Sprintf("Global selfprompt model set to: %s", model))
			}
			return
		}
		if parts[2] == "image" {
			if len(parts) < 4 {
				bot.Send(message.Chat, "Please specify an image model name")
				return
			}
			var chatID *int64
			if len(parts) >= 5 {
				parsedID, err := strconv.ParseInt(parts[4], 10, 64)
				if err == nil {
					chatID = &parsedID
				}
			}
			model := parts[3]
			err := registry.SetImageAiModel(chatID, model)
			if err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error setting image model: %v", err))
				return
			}
			if chatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Image model for chat %d set to: %s", *chatID, model))
			} else {
				bot.Send(message.Chat, fmt.Sprintf("Global image model set to: %s", model))
			}
			return
		}

		var chatID *int64
		if len(parts) >= 4 {
			parsedID, err := strconv.ParseInt(parts[3], 10, 64)
			if err == nil {
				chatID = &parsedID
			}
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
		var chatID *int64
		if len(parts) >= 3 {
			parsedID, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				chatID = &parsedID
			}
		}
		provider := registry.GetAiProvider(chatID)
		model := registry.GetAiModel(chatID)
		imageModel := registry.GetImageAiModel(chatID)
		selfpromptModel := selfpromptplugin.GetModel(chatID)
		if selfpromptModel == "" {
			selfpromptModel = "(default AI model)"
		}

		var response string
		if chatID != nil {
			response = fmt.Sprintf("AI settings for chat %d:\nProvider: %s\nModel: %s\nImage model: %s\nSelfprompt model: %s", *chatID, provider, model, imageModel, selfpromptModel)
		} else {
			response = fmt.Sprintf("Global AI settings:\nProvider: %s\nModel: %s\nImage model: %s\nSelfprompt model: %s", provider, model, imageModel, selfpromptModel)
		}

		bot.Send(message.Chat, response)

	default:
		bot.Send(message.Chat, "Unknown AI command. Available commands: provider, model, get")
	}
}

// handleSpotifyCommands processes commands related to Spotify plugin settings
func (p *AdminPlugin) handleSpotifyCommands(message *telebot.Message) {
	bot := registry.Bot
	parts := strings.Split(message.Text, " ")

	if len(parts) < 2 {
		bot.Send(message.Chat, "Usage:\n!spotify enable [chat_id]\n!spotify disable [chat_id]\n!spotify status [chat_id]\n!spotify desc enable [chat_id]\n!spotify desc disable [chat_id]\n!spotify model <model_name> [chat_id]\n!spotify regenerate <spotify_id_or_url>")
		return
	}

	// Parse chat_id if provided
	var chatID *int64
	if len(parts) >= 3 {
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err == nil {
			chatID = &id
		}
	}

	switch parts[1] {
	case "enable":
		var err error
		if chatID != nil {
			err = spotify.EnableForChat(*chatID)
		} else {
			err = spotify.EnableGlobally()
		}

		if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error enabling Spotify plugin: %v", err))
			return
		}

		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("Spotify plugin enabled for chat %d", *chatID))
		} else {
			bot.Send(message.Chat, "Spotify plugin enabled globally")
		}

	case "disable":
		var err error
		if chatID != nil {
			err = spotify.DisableForChat(*chatID)
		} else {
			err = spotify.DisableGlobally()
		}

		if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error disabling Spotify plugin: %v", err))
			return
		}

		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("Spotify plugin disabled for chat %d", *chatID))
		} else {
			bot.Send(message.Chat, "Spotify plugin disabled globally")
		}

	case "status":
		enabled := registry.GetPluginSetting(chatID, "spotify", "enabled", "true")
		status := "disabled"
		if enabled == "true" {
			status = "enabled"
		}

		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("Spotify plugin is %s for chat %d", status, *chatID))
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Spotify plugin is %s globally", status))
		}

		// Also check if Spotify credentials are configured
		if registry.Config.SpotifyConfig.ClientID == "" || registry.Config.SpotifyConfig.ClientSecret == "" {
			bot.Send(message.Chat, "⚠️ Spotify credentials are not configured in config.yml")
		}

	case "desc":
		if len(parts) < 3 {
			bot.Send(message.Chat, "Usage: !spotify desc enable [chat_id] | !spotify desc disable [chat_id]")
			return
		}
		action := parts[2]
		// Re-parse chat id for desc subcommand from position 3
		var descChatID *int64
		if len(parts) >= 4 {
			if id, err := strconv.ParseInt(parts[3], 10, 64); err == nil {
				descChatID = &id
			}
		}
		switch action {
		case "enable":
			var err error
			if descChatID != nil {
				err = spotify.EnableReviewsForChat(*descChatID)
			} else {
				err = spotify.EnableReviewsGlobally()
			}
			if err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error enabling Spotify reviews: %v", err))
				return
			}
			if descChatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Spotify reviews enabled for chat %d", *descChatID))
			} else {
				bot.Send(message.Chat, "Spotify reviews enabled globally")
			}
		case "disable":
			var err error
			if descChatID != nil {
				err = spotify.DisableReviewsForChat(*descChatID)
			} else {
				err = spotify.DisableReviewsGlobally()
			}
			if err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error disabling Spotify reviews: %v", err))
				return
			}
			if descChatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Spotify reviews disabled for chat %d", *descChatID))
			} else {
				bot.Send(message.Chat, "Spotify reviews disabled globally")
			}
		default:
			bot.Send(message.Chat, "Unknown desc command. Use: enable or disable")
		}
	case "model":
		if len(parts) < 3 {
			bot.Send(message.Chat, "Usage: !spotify model <model_name> [chat_id]")
			return
		}

		model := strings.TrimSpace(parts[2])
		if model == "" {
			bot.Send(message.Chat, "Please specify a model name")
			return
		}

		var modelChatID *int64
		if len(parts) >= 4 {
			id, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil {
				bot.Send(message.Chat, "Invalid chat_id")
				return
			}
			modelChatID = &id
		}

		err := spotify.SetReviewModel(modelChatID, model)
		if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error setting Spotify review model: %v", err))
			return
		}

		if modelChatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("Spotify review model for chat %d set to: %s", *modelChatID, model))
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Global Spotify review model set to: %s", model))
		}
	case "regenerate":
		if len(parts) < 3 {
			bot.Send(message.Chat, "Usage: !spotify regenerate <spotify_id_or_url>")
			return
		}

		spotifyID, err := spotify.ExtractSpotifyID(parts[2])
		if err != nil {
			bot.Send(message.Chat, "Please provide a valid Spotify album/track ID or URL")
			return
		}

		// Call the regenerate function
		reviewURL, err := spotify.RegenerateReview(message.Chat.ID, spotifyID)
		if err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Failed to regenerate review: %v", err))
			return
		}

		bot.Send(message.Chat, fmt.Sprintf("✅ Review regenerated successfully: %s", reviewURL))
	default:
		bot.Send(message.Chat, "Unknown Spotify command. Available commands: enable, disable, status, desc, model, regenerate")
	}
}
