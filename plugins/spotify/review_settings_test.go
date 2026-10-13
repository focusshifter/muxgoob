package spotify

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func setupReviewSettingsTest(t *testing.T) {
	t.Helper()
	oldConfig, oldDB := registry.Config, database.DB
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() { database.DB = oldDB; registry.Config = oldConfig; db.Close() })
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	registry.Config.SpotifyReviewCandidateModels = []string{"yaml/a", "yaml/b"}
	registry.Config.SpotifyReviewJudgeModel = "yaml/judge"
}

func TestReviewSettingsScopedPrecedence(t *testing.T) {
	setupReviewSettingsTest(t)
	chat := int64(-100123)
	check := func(scope *int64, mode string, models []string, judge string) {
		t.Helper()
		got := GetReviewSettings(scope)
		if got.Mode != mode || !reflect.DeepEqual(got.Models, models) || got.JudgeModel != judge {
			t.Fatalf("settings = %+v, want %s %v %s", got, mode, models, judge)
		}
	}
	check(&chat, "ensemble", []string{"yaml/a", "yaml/b"}, "yaml/judge")
	// Existing rows must override the YAML ensemble, not silently enable it.
	if err := registry.SetPluginSetting(nil, SpotifyPluginName, SpotifyReviewModelKey, "legacy/model:exact"); err != nil {
		t.Fatal(err)
	}
	check(&chat, "single", []string{"legacy/model:exact"}, "yaml/judge")
	if err := SetReviewModels(nil, []string{"global/a", "global/b", "global/c:free"}); err != nil {
		t.Fatal(err)
	}
	check(&chat, "ensemble", []string{"global/a", "global/b", "global/c:free"}, "yaml/judge")
	if err := SetReviewModel(&chat, "chat/one:suffix"); err != nil {
		t.Fatal(err)
	}
	if err := SetReviewModels(nil, []string{"later/a", "later/b"}); err != nil {
		t.Fatal(err)
	}
	check(&chat, "single", []string{"chat/one:suffix"}, "yaml/judge")
	if got := GetReviewModel(&chat); got != "chat/one:suffix" {
		t.Fatal(got)
	}
	if err := SetReviewJudge(nil, "global/judge:exact"); err != nil {
		t.Fatal(err)
	}
	check(&chat, "single", []string{"chat/one:suffix"}, "global/judge:exact")
	if err := SetReviewJudge(&chat, "chat/judge"); err != nil {
		t.Fatal(err)
	}
	if err := SetReviewModels(&chat, []string{"chat/a", "chat/b"}); err != nil {
		t.Fatal(err)
	}
	check(&chat, "ensemble", []string{"chat/a", "chat/b"}, "chat/judge")
	if err := SetReviewModel(&chat, "single/again"); err != nil {
		t.Fatal(err)
	}
	check(&chat, "single", []string{"single/again"}, "chat/judge")
	if err := ClearReviewModelOverride(chat); err != nil {
		t.Fatal(err)
	}
	check(&chat, "ensemble", []string{"later/a", "later/b"}, "chat/judge")
	registry.Config.SpotifyReviewJudgeModel = ""
	if err := registry.ClearGlobalPluginSetting(SpotifyPluginName, SpotifyReviewJudgeModelKey); err != nil {
		t.Fatal(err)
	}
	check(nil, "ensemble", []string{"later/a", "later/b"}, defaultSpotifyReviewJudge)
}

func TestReviewSettingsValidationAndAtomicity(t *testing.T) {
	setupReviewSettingsTest(t)
	if err := SetReviewModels(nil, []string{"old/a", "old/b"}); err != nil {
		t.Fatal(err)
	}
	for _, models := range [][]string{nil, {""}, {" a"}, {"a b"}, {"a", "a"}, {"a", "b", "c", "d"}} {
		if err := SetReviewModels(nil, models); err == nil {
			t.Errorf("accepted %q", models)
		}
	}
	if err := SetReviewJudge(nil, ""); err == nil {
		t.Fatal("accepted empty judge")
	}
	if _, err := database.DB.Exec(`CREATE TRIGGER reject_review_models BEFORE INSERT ON plugin_settings WHEN NEW.key = 'review_models' BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SetReviewModel(nil, "new/a"); err == nil {
		t.Fatal("expected transaction failure")
	}
	got := GetReviewSettings(nil)
	if got.Mode != "ensemble" || !reflect.DeepEqual(got.Models, []string{"old/a", "old/b"}) || GetReviewModel(nil) != "old/a" {
		t.Fatalf("partial update: %+v", got)
	}
}

func TestScopedRuntimeGenerationProvenance(t *testing.T) {
	setupReviewSettingsTest(t)
	deps, _ := ensembleTestDeps(t)
	chat := int64(-987)
	models := []string{"provider/first:exact", "provider/second:suffix"}
	if err := SetReviewModels(&chat, models); err != nil {
		t.Fatal(err)
	}
	if err := SetReviewJudge(&chat, "provider/judge:exact"); err != nil {
		t.Fatal(err)
	}
	deps.complete = func(ctx context.Context, model string, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		if req.ResponseFormat.JSONSchema.Name == "spotify_review_judge" {
			if model != "provider/judge:exact" {
				t.Errorf("judge = %s", model)
			}
			return completionResponse(`{"selected_id":"candidate_1","reason":"selected","rejected_ids":[]}`), nil
		}
		if model != models[0] && model != models[1] {
			t.Errorf("unexpected author %s", model)
		}
		return ensembleResponse(ensembleBody("review", 160), 7.25), nil
	}
	_, _, generatedBy, err := generateSpotifyReviewWithDeps(context.Background(), &chat, "album", "Artist", "Title", "2026", nil, spotifyReviewOptionsForChat(&chat), deps)
	if err != nil {
		t.Fatal(err)
	}
	if generatedBy != models[1] {
		t.Fatalf("provenance = %s", generatedBy)
	}
	if err := SetReviewModel(&chat, "provider/single:suffix"); err != nil {
		t.Fatal(err)
	}
	deps.legacy = func(ctx context.Context, scope *int64, prompt, typ string) (string, sql.NullFloat64, string, error) {
		model := resolveSpotifyReviewModel(scope)
		if model != "provider/single:suffix" {
			t.Errorf("single model = %s", model)
		}
		return "single", sql.NullFloat64{}, spotifyReviewGeneratedModelID(model, model), nil
	}
	deps.complete = func(context.Context, string, openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
		t.Error("single invoked ensemble/judge")
		return openai.ChatCompletionResponse{}, nil
	}
	_, _, generatedBy, err = generateSpotifyReviewWithDeps(context.Background(), &chat, "album", "Artist", "Title", "2026", nil, spotifyReviewOptionsForChat(&chat), deps)
	if err != nil || generatedBy != "provider/single:suffix" {
		t.Fatalf("single provenance=%s err=%v", generatedBy, err)
	}
}

func TestReviewOptionsUseEffectiveScope(t *testing.T) {
	setupReviewSettingsTest(t)
	chat := int64(-42)
	if err := SetReviewModel(&chat, "runtime/one"); err != nil {
		t.Fatal(err)
	}
	if got := spotifyReviewOptionsForChat(&chat); len(got.Models) != 0 {
		t.Fatalf("single used ensemble: %+v", got)
	}
	if got := spotifyReviewOptionsForChat(nil); !reflect.DeepEqual(got.Models, []string{"yaml/a", "yaml/b"}) {
		t.Fatal(got)
	}
}
