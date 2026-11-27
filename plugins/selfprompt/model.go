package selfprompt

import (
	"strings"

	"github.com/focusshifter/muxgoob/registry"
)

const (
	PluginName           = "selfprompt"
	ModelKey             = "model"
	legacyCompressionKey = "compression_model"
	// A conservative byte-volume safeguard, not a storage or model/API limit.
	defaultCompactChars = 16 * 1024
	// Retain the legacy accessor without a zero sentinel that would trigger OR callers.
	defaultCompactBullets = int(^uint(0) >> 1)
)

// DefaultCompactChars returns the consolidation safeguard in bytes (not runes).
func DefaultCompactChars() int {
	return defaultCompactChars
}

// DefaultCompactBullets is deprecated: bullet count no longer triggers consolidation.
// The maximum int keeps legacy >= comparisons effectively disabled.
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
