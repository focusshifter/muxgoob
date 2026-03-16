package selfprompt

import (
	"strings"

	"github.com/focusshifter/muxgoob/registry"
)

const (
	PluginName            = "selfprompt"
	CompressionModelKey   = "compression_model"
	defaultCompactChars   = 2200
	defaultCompactBullets = 12
)

func DefaultCompactChars() int {
	return defaultCompactChars
}

func DefaultCompactBullets() int {
	return defaultCompactBullets
}

func GetCompressionModel(chatID *int64) string {
	return strings.TrimSpace(registry.GetPluginSetting(chatID, PluginName, CompressionModelKey, ""))
}

func SetCompressionModel(chatID *int64, model string) error {
	return registry.SetPluginSetting(chatID, PluginName, CompressionModelKey, strings.TrimSpace(model))
}
