package registry

import (
	"database/sql"
	"log"
	"strconv"
	"strings"

	"github.com/focusshifter/muxgoob/database"
)

const (
	// ConfigPluginName is the name used for global configuration settings in the database
	ConfigPluginName = "config"

	// AI provider and model keys
	AiProviderKey             = "ai_provider"
	AiModelKey                = "ai_model"
	ImageAiModelKey           = "image_ai_model"
	ImageVisionModelKey       = "image_vision_model"
	ImageAiProviderKey        = "image_ai_provider"
	ImageAiSizeKey            = "image_ai_size"
	ImagePromptProviderKey    = "image_prompt_provider"
	ImagePromptModelKey       = "image_prompt_model"
	ImagePromptModeKey        = "image_prompt_mode"
	ImageGenerationEnabledKey = "image_generation_enabled"
)

// init is called after database initialization
// We use a variable to track if the table has been initialized
var dbInitialized bool = false

// InitializeDbSettings ensures the plugin_settings table exists
// This should be called after database.Initialize() in main.go
func InitializeDbSettings() {
	if dbInitialized {
		return
	}

	// Ensure the database is initialized
	if database.DB == nil {
		log.Printf("[registry] Database not initialized yet, skipping plugin_settings table creation")
		return
	}

	err := EnsurePluginSettingsTable()
	if err != nil {
		log.Printf("[registry] Failed to create plugin_settings table: %v", err)
		return
	}

	dbInitialized = true
	log.Printf("[registry] Plugin settings table initialized")
}

// EnsurePluginSettingsTable ensures the plugin_settings table exists
func EnsurePluginSettingsTable() error {
	_, err := database.DB.Exec(`
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
		log.Printf("[registry] Error creating plugin_settings table: %v", err)
	}
	return err
}

// GetPluginSetting retrieves a setting from the database with fallback to default value
func GetPluginSetting(chatID *int64, pluginName string, key string, defaultValue string) string {
	if database.DB == nil {
		return defaultValue
	}
	var value string
	var err error

	// First try to get chat-specific setting if chatID is provided
	if chatID != nil {
		err = database.DB.QueryRow(`
			SELECT value FROM plugin_settings
			WHERE plugin_name = ? AND key = ? AND chat_id = ?`,
			pluginName, key, *chatID).Scan(&value)
	}

	// If no chat-specific setting or no chatID provided, try global setting
	if chatID == nil || err == sql.ErrNoRows {
		err = database.DB.QueryRow(`
			SELECT value FROM plugin_settings
			WHERE plugin_name = ? AND key = ? AND chat_id IS NULL`,
			pluginName, key).Scan(&value)
	}

	// If not found in database, return default
	if err == sql.ErrNoRows {
		return defaultValue
	} else if err != nil {
		log.Printf("[registry] Error getting setting %s.%s: %v", pluginName, key, err)
		return defaultValue
	}

	return value
}

// PluginSettingOverrides contains only persisted values; nil means that scope has no row.
type PluginSettingOverrides struct {
	Global *string
	Chat   *string
}

// GetPluginSettingOverrides exposes global and chat-specific persisted values without
// applying config fallback. It is intended for diagnostics/admin UIs.
func GetPluginSettingOverrides(chatID int64, pluginName string, key string) (PluginSettingOverrides, error) {
	if database.DB == nil {
		return PluginSettingOverrides{}, nil
	}
	result := PluginSettingOverrides{}
	var global string
	err := database.DB.QueryRow(`SELECT value FROM plugin_settings WHERE chat_id IS NULL AND plugin_name = ? AND key = ?`, pluginName, key).Scan(&global)
	if err == nil {
		result.Global = &global
	} else if err != sql.ErrNoRows {
		return result, err
	}
	var chat string
	err = database.DB.QueryRow(`SELECT value FROM plugin_settings WHERE chat_id = ? AND plugin_name = ? AND key = ?`, chatID, pluginName, key).Scan(&chat)
	if err == nil {
		result.Chat = &chat
	} else if err != sql.ErrNoRows {
		return result, err
	}
	return result, nil
}

// ClearPluginSettingOverride removes a chat-specific setting so it inherits the
// global database value or its config fallback.
func ClearPluginSettingOverride(chatID int64, pluginName string, key string) error {
	if database.DB == nil {
		return nil
	}
	_, err := database.DB.Exec(`DELETE FROM plugin_settings WHERE chat_id = ? AND plugin_name = ? AND key = ?`, chatID, pluginName, key)
	return err
}

// SetPluginSetting saves a setting to the database
func SetPluginSetting(chatID *int64, pluginName string, key string, value string) error {
	// Handle global settings (NULL chat_id) specially
	if chatID == nil {
		// First check if a global setting already exists
		var count int
		err := database.DB.QueryRow(`
			SELECT COUNT(*) FROM plugin_settings
			WHERE chat_id IS NULL AND plugin_name = ? AND key = ?`,
			pluginName, key).Scan(&count)

		if err != nil {
			log.Printf("[registry] Error checking for existing global setting %s.%s: %v", pluginName, key, err)
			return err
		}

		// If it exists, update it
		if count > 0 {
			_, err = database.DB.Exec(`
				UPDATE plugin_settings SET value = ?
				WHERE chat_id IS NULL AND plugin_name = ? AND key = ?`,
				value, pluginName, key)
		} else {
			// Otherwise insert a new row
			_, err = database.DB.Exec(`
				INSERT INTO plugin_settings (chat_id, plugin_name, key, value)
				VALUES (NULL, ?, ?, ?)`,
				pluginName, key, value)
		}

		if err != nil {
			log.Printf("[registry] Error setting global %s.%s: %v", pluginName, key, err)
		}
		return err
	}

	// For chat-specific settings, we can use the UNIQUE constraint
	_, err := database.DB.Exec(`
		INSERT INTO plugin_settings (chat_id, plugin_name, key, value)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(chat_id, plugin_name, key) DO UPDATE SET value = ?`,
		chatID, pluginName, key, value, value)

	if err != nil {
		log.Printf("[registry] Error setting %s.%s for chat %d: %v", pluginName, key, *chatID, err)
	}
	return err
}

// GetAiProvider returns the AI provider from database or falls back to config.yml
func GetAiProvider(chatID *int64) string {
	return GetPluginSetting(chatID, ConfigPluginName, AiProviderKey, Config.AiProvider)
}

// GetAiModel returns the AI model from database or falls back to config.yml
func GetAiModel(chatID *int64) string {
	return GetPluginSetting(chatID, ConfigPluginName, AiModelKey, Config.AiModel)
}

// GetImageAiModel returns the image AI model from database or falls back to config.yml
func GetImageAiModel(chatID *int64) string {
	return GetPluginSetting(chatID, ConfigPluginName, ImageAiModelKey, Config.ImageAiModel)
}

// GetImageVisionModel returns the chat-completions model used to inspect incoming images.
// It is intentionally independent from ImageAiModel, which selects an image generator.
func GetImageVisionModel(chatID *int64) string {
	defaultModel := strings.TrimSpace(Config.ImageVisionModel)
	if defaultModel == "" {
		defaultModel = "google/gemini-3.1-flash-lite-preview"
	}
	return GetPluginSetting(chatID, ConfigPluginName, ImageVisionModelKey, defaultModel)
}

// GetImageAiProvider returns the image provider from database or falls back to config.yml.
// openai-codex preserves the historic Codex image-generation behavior.
func GetImageAiProvider(chatID *int64) string {
	defaultProvider := strings.TrimSpace(Config.ImageAiProvider)
	if defaultProvider == "" {
		defaultProvider = "openai-codex"
	}
	return GetPluginSetting(chatID, ConfigPluginName, ImageAiProviderKey, defaultProvider)
}

// GetImageAiSize returns an optional preferred pixel size such as 2048x2048.
// An empty value means that Gooby chooses a compatible default for the model.
func GetImageAiSize(chatID *int64) string {
	return GetPluginSetting(chatID, ConfigPluginName, ImageAiSizeKey, Config.ImageAiSize)
}

// GetImagePromptProvider returns the text provider that composes prompts for direct image requests.
func GetImagePromptProvider(chatID *int64) string {
	return GetPluginSetting(chatID, ConfigPluginName, ImagePromptProviderKey, Config.ImagePromptProvider)
}

// GetImagePromptModel returns the text model that composes prompts for direct image requests.
func GetImagePromptModel(chatID *int64) string {
	return GetPluginSetting(chatID, ConfigPluginName, ImagePromptModelKey, Config.ImagePromptModel)
}

// GetImagePromptMode returns off, direct, or fallback. Invalid values are treated as off.
func GetImagePromptMode(chatID *int64) string {
	mode := strings.ToLower(strings.TrimSpace(GetPluginSetting(chatID, ConfigPluginName, ImagePromptModeKey, Config.ImagePromptMode)))
	switch mode {
	case "direct", "fallback":
		return mode
	default:
		return "off"
	}
}

// SetAiProvider sets the AI provider in the database
func SetAiProvider(chatID *int64, provider string) error {
	return SetPluginSetting(chatID, ConfigPluginName, AiProviderKey, provider)
}

// SetAiModel sets the AI model in the database
func SetAiModel(chatID *int64, model string) error {
	return SetPluginSetting(chatID, ConfigPluginName, AiModelKey, model)
}

// ClearAiModelOverride removes a chat-specific model so the chat inherits the global model.
func ClearAiModelOverride(chatID int64) error {
	return ClearPluginSettingOverride(chatID, ConfigPluginName, AiModelKey)
}

// SetImageAiModel sets the image AI model in the database
func SetImageAiModel(chatID *int64, model string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImageAiModelKey, model)
}

// SetImageVisionModel sets the vision-capable chat-completions model for image inspection.
func SetImageVisionModel(chatID *int64, model string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImageVisionModelKey, model)
}

// SetImageAiProvider sets the image provider in the database.
func SetImageAiProvider(chatID *int64, provider string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImageAiProviderKey, provider)
}

// SetImageAiSize sets an optional preferred pixel size. An empty string enables auto size.
func SetImageAiSize(chatID *int64, size string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImageAiSizeKey, size)
}

// SetImagePromptProvider sets the text provider that composes direct image prompts.
func SetImagePromptProvider(chatID *int64, provider string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImagePromptProviderKey, provider)
}

// SetImagePromptModel sets the text model that composes direct image prompts.
func SetImagePromptModel(chatID *int64, model string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImagePromptModelKey, model)
}

// SetImagePromptMode sets how the image prompt composer is used.
func SetImagePromptMode(chatID *int64, mode string) error {
	return SetPluginSetting(chatID, ConfigPluginName, ImagePromptModeKey, mode)
}

// GetImageGenerationEnabled returns whether the generateImage tool is allowed for a chat.
// Image generation is opt-in per chat; the default is disabled so the tool is not exposed
// unless an admin explicitly enables it for that chat ID.
func GetImageGenerationEnabled(chatID int64) bool {
	if database.DB == nil {
		return false
	}
	value := GetPluginSetting(&chatID, ConfigPluginName, ImageGenerationEnabledKey, "false")
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("[registry] Invalid %s value for chat %d: %q", ImageGenerationEnabledKey, chatID, value)
		return false
	}
	return enabled
}

// SetImageGenerationEnabled sets whether a chat may use the generateImage tool.
func SetImageGenerationEnabled(chatID int64, enabled bool) error {
	return SetPluginSetting(&chatID, ConfigPluginName, ImageGenerationEnabledKey, strconv.FormatBool(enabled))
}
