package selfprompt

import (
	"strings"

	"github.com/focusshifter/muxgoob/registry"
)

const (
	PluginName            = "selfprompt"
	ModelKey              = "model"
	legacyCompressionKey  = "compression_model"
	defaultCompactChars   = 2200
	defaultCompactBullets = 12
)

func DefaultCompactChars() int {
	return defaultCompactChars
}

func DefaultCompactBullets() int {
	return defaultCompactBullets
}

func GetModel(chatID *int64) string {
	model := strings.TrimSpace(registry.GetPluginSetting(chatID, PluginName, ModelKey, ""))
	if model != "" {
		return model
	}
	return strings.TrimSpace(registry.GetPluginSetting(chatID, PluginName, legacyCompressionKey, ""))
}

func SetModel(chatID *int64, model string) error {
	if err := registry.SetPluginSetting(chatID, PluginName, ModelKey, strings.TrimSpace(model)); err != nil {
		return err
	}
	if chatID == nil {
		return registry.ClearGlobalPluginSetting(PluginName, legacyCompressionKey)
	}
	return registry.ClearPluginSettingOverride(*chatID, PluginName, legacyCompressionKey)
}
