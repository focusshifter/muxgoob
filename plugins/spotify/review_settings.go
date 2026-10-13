package spotify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"unicode"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

const (
	SpotifyReviewModelsKey     = "review_models"
	SpotifyReviewModeKey       = "review_mode"
	SpotifyReviewJudgeModelKey = "review_judge_model"
)

// ReviewSettings is the effective author configuration for a scope. JudgeModel
// inherits independently and is not invoked in single mode.
type ReviewSettings struct {
	Mode       string
	Models     []string
	JudgeModel string
}

func validateReviewModels(models []string) error {
	if len(models) < 1 || len(models) > 3 {
		return fmt.Errorf("specify 1–3 models")
	}
	seen := map[string]bool{}
	for _, model := range models {
		if model == "" || strings.IndexFunc(model, unicode.IsSpace) >= 0 {
			return fmt.Errorf("model IDs must be nonblank and contain no whitespace")
		}
		if seen[model] {
			return fmt.Errorf("duplicate model: %s", model)
		}
		seen[model] = true
	}
	return nil
}

// SetReviewModels atomically replaces mode, authors, and the legacy primary ID.
// Delete/insert also handles SQLite's non-unique NULL global scope correctly.
func SetReviewModels(chatID *int64, models []string) error {
	if err := validateReviewModels(models); err != nil {
		return err
	}
	mode := "single"
	if len(models) > 1 {
		mode = "ensemble"
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return err
	}
	return writeReviewModelSettings(chatID, map[string]string{SpotifyReviewModeKey: mode, SpotifyReviewModelsKey: string(encoded), SpotifyReviewModelKey: models[0]})
}

func writeReviewModelSettings(chatID *int64, values map[string]string) error {
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range []string{SpotifyReviewModeKey, SpotifyReviewModelsKey, SpotifyReviewModelKey} {
		if _, err = tx.Exec(`DELETE FROM plugin_settings WHERE chat_id IS ? AND plugin_name = ? AND key = ?`, chatID, SpotifyPluginName, key); err != nil {
			return err
		}
		if value, ok := values[key]; ok {
			if _, err = tx.Exec(`INSERT INTO plugin_settings (chat_id,plugin_name,key,value) VALUES (?,?,?,?)`, chatID, SpotifyPluginName, key, value); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func ClearReviewModelOverride(chatID int64) error {
	return writeReviewModelSettings(&chatID, nil)
}

func SetReviewJudge(chatID *int64, model string) error {
	if err := validateReviewModels([]string{model}); err != nil {
		return err
	}
	if database.DB == nil {
		return fmt.Errorf("database not initialized")
	}
	return registry.SetPluginSetting(chatID, SpotifyPluginName, SpotifyReviewJudgeModelKey, model)
}

// Read all relevant rows in one snapshot; choose a complete model override from
// chat before global, never independently inherit mode/list across scopes.
func GetReviewSettings(chatID *int64) ReviewSettings {
	global, chat := map[string]string{}, map[string]string{}
	if database.DB != nil {
		rows, err := database.DB.Query(`SELECT chat_id,key,value FROM plugin_settings WHERE plugin_name = ? AND (chat_id IS NULL OR chat_id = ?) ORDER BY id`, SpotifyPluginName, chatID)
		if err != nil {
			log.Printf("[spotify] Error reading review settings: %v", err)
		} else {
			for rows.Next() {
				var scope sql.NullInt64
				var key, value string
				if err := rows.Scan(&scope, &key, &value); err != nil {
					log.Printf("[spotify] Error reading review setting: %v", err)
					continue
				}
				if scope.Valid {
					chat[key] = value
				} else {
					global[key] = value
				}
			}
			if err := rows.Err(); err != nil {
				log.Printf("[spotify] Error reading review settings: %v", err)
			}
			rows.Close()
		}
	}
	modelsFor := func(values map[string]string) []string {
		if value, ok := values[SpotifyReviewModelsKey]; ok {
			var models []string
			if json.Unmarshal([]byte(value), &models) == nil && validateReviewModels(models) == nil {
				return models
			}
		}
		if model := values[SpotifyReviewModelKey]; strings.TrimSpace(model) != "" {
			return []string{model}
		}
		return nil
	}
	models := modelsFor(chat)
	if len(models) == 0 {
		models = modelsFor(global)
	}
	if len(models) == 0 && len(registry.Config.SpotifyReviewCandidateModels) > 0 {
		models = append([]string(nil), registry.Config.SpotifyReviewCandidateModels...)
	}
	if len(models) == 0 {
		models = []string{resolveSpotifyDefaultReviewModel(chatID)}
	}
	mode := "single"
	if len(models) > 1 {
		mode = "ensemble"
	}
	judge := chat[SpotifyReviewJudgeModelKey]
	if judge == "" {
		judge = global[SpotifyReviewJudgeModelKey]
	}
	if judge == "" {
		judge = registry.Config.SpotifyReviewJudgeModel
	}
	if judge == "" {
		judge = defaultSpotifyReviewJudge
	}
	return ReviewSettings{Mode: mode, Models: models, JudgeModel: judge}
}
