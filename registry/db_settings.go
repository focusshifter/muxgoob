package registry

import (
	"database/sql"
	"log"

	"github.com/focusshifter/muxgoob/database"
)

const (
	// ConfigPluginName is the name used for global configuration settings in the database
	ConfigPluginName = "config"
	
	// AI provider and model keys
	AiProviderKey = "ai_provider"
	AiModelKey    = "ai_model"
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

// SetAiProvider sets the AI provider in the database
func SetAiProvider(chatID *int64, provider string) error {
	return SetPluginSetting(chatID, ConfigPluginName, AiProviderKey, provider)
}

// SetAiModel sets the AI model in the database
func SetAiModel(chatID *int64, model string) error {
	return SetPluginSetting(chatID, ConfigPluginName, AiModelKey, model)
}
