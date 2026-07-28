package admin

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
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
		bot.Send(message.Chat, "Usage:\n!ai provider [openrouter|openai|openai-codex] [chat_id]\n!ai provider image [openai-codex|openrouter] [chat_id]\n!ai provider image-prompt openrouter [chat_id]\n!ai model <model_name> [chat_id]\n!ai model global <chat_id>\n!ai model image <model_name> [chat_id]\n!ai image size <WIDTHxHEIGHT|auto> [chat_id]\n!ai model image-prompt <model_name> [chat_id]\n!ai image-prompt mode [off|direct|fallback] [chat_id]\n!ai model selfprompt <model_name> [chat_id]\n!ai images enable <chat_id>\n!ai images disable <chat_id>\n!ai images status <chat_id>\n!ai get [chat_id]")
		return
	}

	switch parts[1] {
	case "provider":
		if len(parts) >= 3 && parts[2] == "image-prompt" {
			if len(parts) < 4 {
				bot.Send(message.Chat, "Usage: !ai provider image-prompt openrouter [chat_id]")
				return
			}
			provider := parts[3]
			if provider != "openrouter" {
				bot.Send(message.Chat, "Invalid image prompt provider. Use 'openrouter'")
				return
			}
			var chatID *int64
			if len(parts) >= 5 {
				if parsedID, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
					chatID = &parsedID
				}
			}
			if err := registry.SetImagePromptProvider(chatID, provider); err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error setting image prompt provider: %v", err))
				return
			}
			if chatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Image prompt provider for chat %d set to: %s", *chatID, provider))
			} else {
				bot.Send(message.Chat, fmt.Sprintf("Global image prompt provider set to: %s", provider))
			}
			return
		}
		if len(parts) >= 3 && parts[2] == "image" {
			if len(parts) < 4 {
				bot.Send(message.Chat, "Usage: !ai provider image [openai-codex|openrouter] [chat_id]")
				return
			}
			provider := parts[3]
			if provider != "openai-codex" && provider != "openrouter" {
				bot.Send(message.Chat, "Invalid image provider. Use 'openai-codex' or 'openrouter'")
				return
			}
			var chatID *int64
			if len(parts) >= 5 {
				parsedID, err := strconv.ParseInt(parts[4], 10, 64)
				if err == nil {
					chatID = &parsedID
				}
			}
			if err := registry.SetImageAiProvider(chatID, provider); err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error setting image provider: %v", err))
				return
			}
			if chatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Image provider for chat %d set to: %s", *chatID, provider))
			} else {
				bot.Send(message.Chat, fmt.Sprintf("Global image provider set to: %s", provider))
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
		if len(parts) < 3 {
			bot.Send(message.Chat, "Please specify a provider (openrouter, openai, or openai-codex)")
			return
		}

		provider := parts[2]
		if provider != "openrouter" && provider != "openai" && provider != "openai-codex" {
			bot.Send(message.Chat, "Invalid provider. Use 'openrouter', 'openai', or 'openai-codex'")
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
		if parts[2] == "global" || parts[2] == "reset" {
			if len(parts) < 4 {
				bot.Send(message.Chat, "Usage: !ai model global <chat_id>")
				return
			}
			chatID, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil {
				bot.Send(message.Chat, "Please specify a valid chat ID")
				return
			}
			if err := registry.ClearAiModelOverride(chatID); err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error clearing AI model override: %v", err))
				return
			}
			bot.Send(message.Chat, fmt.Sprintf("AI model override for chat %d cleared; it now uses the global model: %s", chatID, registry.GetAiModel(nil)))
			return
		}
		if parts[2] == "image-prompt" {
			if len(parts) < 4 {
				bot.Send(message.Chat, "Please specify an image prompt model name")
				return
			}
			var chatID *int64
			if len(parts) >= 5 {
				if parsedID, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
					chatID = &parsedID
				}
			}
			model := parts[3]
			if err := registry.SetImagePromptModel(chatID, model); err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error setting image prompt model: %v", err))
				return
			}
			if chatID != nil {
				bot.Send(message.Chat, fmt.Sprintf("Image prompt model for chat %d set to: %s", *chatID, model))
			} else {
				bot.Send(message.Chat, fmt.Sprintf("Global image prompt model set to: %s", model))
			}
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

	case "image":
		if len(parts) < 4 || parts[2] != "size" {
			bot.Send(message.Chat, "Usage: !ai image size <WIDTHxHEIGHT|auto> [chat_id]")
			return
		}
		size, err := normalizeImageSizeSetting(parts[3])
		if err != nil {
			bot.Send(message.Chat, "Invalid image size. Use positive WIDTHxHEIGHT, for example 2048x2048, or 'auto'")
			return
		}
		var chatID *int64
		if len(parts) >= 5 {
			if parsedID, parseErr := strconv.ParseInt(parts[4], 10, 64); parseErr == nil {
				chatID = &parsedID
			}
		}
		if err := registry.SetImageAiSize(chatID, size); err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error setting image size: %v", err))
			return
		}
		shownSize := size
		if shownSize == "" {
			shownSize = "auto"
		}
		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("Image size for chat %d set to: %s", *chatID, shownSize))
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Global image size set to: %s", shownSize))
		}

	case "image-prompt":
		if len(parts) < 3 || parts[2] != "mode" || len(parts) < 4 {
			bot.Send(message.Chat, "Usage: !ai image-prompt mode [off|direct|fallback] [chat_id]")
			return
		}
		mode := parts[3]
		if mode != "off" && mode != "direct" && mode != "fallback" {
			bot.Send(message.Chat, "Invalid image prompt mode. Use 'off', 'direct', or 'fallback'")
			return
		}
		var chatID *int64
		if len(parts) >= 5 {
			if parsedID, err := strconv.ParseInt(parts[4], 10, 64); err == nil {
				chatID = &parsedID
			}
		}
		if err := registry.SetImagePromptMode(chatID, mode); err != nil {
			bot.Send(message.Chat, fmt.Sprintf("Error setting image prompt mode: %v", err))
			return
		}
		if chatID != nil {
			bot.Send(message.Chat, fmt.Sprintf("Image prompt mode for chat %d set to: %s", *chatID, mode))
		} else {
			bot.Send(message.Chat, fmt.Sprintf("Global image prompt mode set to: %s", mode))
		}

	case "images":
		if len(parts) < 4 {
			bot.Send(message.Chat, "Usage:\n!ai images enable <chat_id>\n!ai images disable <chat_id>\n!ai images status <chat_id>")
			return
		}
		targetChatID, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			bot.Send(message.Chat, "Invalid chat_id")
			return
		}
		switch parts[2] {
		case "enable", "allow", "on":
			if err := registry.SetImageGenerationEnabled(targetChatID, true); err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error enabling image generation: %v", err))
				return
			}
			bot.Send(message.Chat, fmt.Sprintf("Image generation enabled for chat %d", targetChatID))
		case "disable", "deny", "off":
			if err := registry.SetImageGenerationEnabled(targetChatID, false); err != nil {
				bot.Send(message.Chat, fmt.Sprintf("Error disabling image generation: %v", err))
				return
			}
			bot.Send(message.Chat, fmt.Sprintf("Image generation disabled for chat %d", targetChatID))
		case "status":
			status := "disabled"
			if registry.GetImageGenerationEnabled(targetChatID) {
				status = "enabled"
			}
			bot.Send(message.Chat, fmt.Sprintf("Image generation for chat %d: %s", targetChatID, status))
		default:
			bot.Send(message.Chat, "Unknown images command. Use enable, disable, or status")
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
		imageProvider := registry.GetImageAiProvider(chatID)
		imageModel := registry.GetImageAiModel(chatID)
		imageSize := registry.GetImageAiSize(chatID)
		if imageSize == "" {
			imageSize = "auto"
		}
		imagePromptProvider := registry.GetImagePromptProvider(chatID)
		imagePromptModel := registry.GetImagePromptModel(chatID)
		imagePromptMode := registry.GetImagePromptMode(chatID)
		imageGenerationStatus := "disabled"
		if chatID != nil && registry.GetImageGenerationEnabled(*chatID) {
			imageGenerationStatus = "enabled"
		}
		selfpromptModel := selfpromptplugin.GetModel(chatID)
		if selfpromptModel == "" {
			selfpromptModel = "(default AI model)"
		}

		var response string
		if chatID != nil {
			response = fmt.Sprintf("AI settings for chat %d:\nProvider: %s\nModel: %s\nImage provider: %s\nImage model: %s\nImage size: %s\nImage prompt composer: %s / %s (%s)\nImage generation: %s\nSelfprompt model: %s", *chatID, provider, model, imageProvider, imageModel, imageSize, imagePromptProvider, imagePromptModel, imagePromptMode, imageGenerationStatus, selfpromptModel)
		} else {
			response = fmt.Sprintf("Global AI settings:\nProvider: %s\nModel: %s\nImage provider: %s\nImage model: %s\nImage size: %s\nImage prompt composer: %s / %s (%s)\nImage generation: disabled by default, enable per chat with !ai images enable <chat_id>\nSelfprompt model: %s", provider, model, imageProvider, imageModel, imageSize, imagePromptProvider, imagePromptModel, imagePromptMode, selfpromptModel)
		}
		if provider == "openai-codex" {
			response += "\nCodex auth: " + openaicodex.NewClient().AuthStatus()
		}

		bot.Send(message.Chat, response)

	default:
		bot.Send(message.Chat, "Unknown AI command. Available commands: provider, model, get")
	}
}

func normalizeImageSizeSetting(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "auto" {
		return "", nil
	}
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return "", fmt.Errorf("expected WIDTHxHEIGHT")
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width < 1 || height < 1 || width > 8192 || height > 8192 {
		return "", fmt.Errorf("invalid dimensions")
	}
	return fmt.Sprintf("%dx%d", width, height), nil
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
