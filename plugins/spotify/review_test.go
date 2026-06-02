package spotify

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	openai "github.com/sashabaranov/go-openai"
)

func TestBuildSpotifyReviewPrompt_DefaultFallback(t *testing.T) {
	registry.Config = registry.Configuration{}

	prompt := buildSpotifyReviewPrompt("album", "World's End Girlfriend", "Helix of Frequency", "2025", "mixed reception")

	checks := []string{
		"Semi-follow the overall consensus",
		"Do not be automatically harsh",
		"Mention both what works and what does not",
		"mixed reception",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestBuildSpotifyReviewPrompt_CustomTemplate(t *testing.T) {
	registry.Config = registry.Configuration{
		SpotifyReviewPrompt: "Review {artist} - {title} ({year}) as a {type}. Facts: {grounding}",
	}

	prompt := buildSpotifyReviewPrompt("album", "Deer Hunter", "Sunya", "2025", "praised groove, criticized repetition")

	checks := []string{
		"Review Deer Hunter - Sunya (2025) as a album.",
		"praised groove, criticized repetition",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestBuildSpotifyReviewPrompt_AlbumRequiresRatingParagraph(t *testing.T) {
	registry.Config = registry.Configuration{
		SpotifyReviewPrompt: "Review {artist} - {title}. Facts: {grounding}",
	}

	prompt := buildSpotifyReviewPrompt("album", "Deer Hunter", "Sunya", "2025", "praised groove")

	checks := []string{
		"Give the album a numeric rating from 1 to 10 in 0.5 increments",
		"The model must return this rating in the structured album_rating field",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected album prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestBuildSpotifyReviewPrompt_TrackDoesNotRequireAlbumRating(t *testing.T) {
	registry.Config = registry.Configuration{
		SpotifyReviewPrompt: "Review {artist} - {title}. Facts: {grounding}",
	}

	prompt := buildSpotifyReviewPrompt("track", "Deer Hunter", "Sunya", "2025", "praised groove")

	if strings.Contains(prompt, "Give the album a numeric rating") {
		t.Fatalf("did not expect track prompt to contain album rating instruction, got %q", prompt)
	}
}

func TestBuildSpotifyReviewCompletionRequest_AlbumUsesStructuredSchema(t *testing.T) {
	req := buildSpotifyReviewCompletionRequest("test-model", "prompt", "album")

	if req.ResponseFormat == nil {
		t.Fatalf("expected structured response format")
	}
	if req.ResponseFormat.Type != openai.ChatCompletionResponseFormatTypeJSONSchema {
		t.Fatalf("expected json_schema response format, got %q", req.ResponseFormat.Type)
	}
	if req.ResponseFormat.JSONSchema == nil || !req.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("expected strict JSON schema, got %#v", req.ResponseFormat.JSONSchema)
	}

	schemaBytes, err := req.ResponseFormat.JSONSchema.Schema.MarshalJSON()
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("failed to unmarshal schema: %v", err)
	}

	properties := schema["properties"].(map[string]any)
	if _, ok := properties["review_text"]; !ok {
		t.Fatalf("expected review_text property in schema: %v", schema)
	}
	albumRating := properties["album_rating"].(map[string]any)
	if albumRating["minimum"] != float64(1) || albumRating["maximum"] != float64(10) {
		t.Fatalf("expected album_rating range 1..10, got %v", albumRating)
	}
}

func TestParseSpotifyReviewCompletion_AssemblesAlbumRatingParagraph(t *testing.T) {
	content := `{"review_text":"Смешной, но неровный альбом.","album_rating":4.5}`

	review, rating, err := parseSpotifyReviewCompletion(content, "album")
	if err != nil {
		t.Fatalf("parseSpotifyReviewCompletion returned error: %v", err)
	}

	if review != "Смешной, но неровный альбом.\n\n4,5 / 10" {
		t.Fatalf("unexpected assembled review: %q", review)
	}
	if !rating.Valid || rating.Float64 != 4.5 {
		t.Fatalf("expected rating 4.5, got valid=%v value=%v", rating.Valid, rating.Float64)
	}
}

func TestParseSpotifyReviewCompletion_RejectsAlbumRatingOutsideHalfStep(t *testing.T) {
	content := `{"review_text":"Неровно.","album_rating":4.2}`

	if _, _, err := parseSpotifyReviewCompletion(content, "album"); err == nil {
		t.Fatal("expected non-half-step album rating to fail")
	}
}

func TestSaveReviewText_StoresAlbumRatingBesideReviewText(t *testing.T) {
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() {
		database.DB = nil
		_ = db.Close()
	})
	createSpotifyReviewsTable(t, db)

	review := "Смешной, но неровный альбом.\n\n4,5 / 10"
	if err := saveReviewText("album", "spotify:album:1", review, sqlRating(4.5)); err != nil {
		t.Fatalf("saveReviewText returned error: %v", err)
	}

	var storedReview string
	var rating sql.NullFloat64
	if err := db.QueryRow(
		"SELECT review_text, album_rating FROM spotify_reviews WHERE type = ? AND item_key = ?",
		"album", "spotify:album:1",
	).Scan(&storedReview, &rating); err != nil {
		t.Fatalf("failed to read stored review: %v", err)
	}

	if storedReview != review {
		t.Fatalf("expected review text %q, got %q", review, storedReview)
	}
	if !rating.Valid || rating.Float64 != 4.5 {
		t.Fatalf("expected album rating 4.5, got valid=%v value=%v", rating.Valid, rating.Float64)
	}
}

func TestSaveReviewText_DoesNotStoreRatingForTrackReview(t *testing.T) {
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() {
		database.DB = nil
		_ = db.Close()
	})
	createSpotifyReviewsTable(t, db)

	review := "Сингл бодрый.\n\n4,5 / 10"
	if err := saveReviewText("track", "spotify:track:1", review, sqlRating(4.5)); err != nil {
		t.Fatalf("saveReviewText returned error: %v", err)
	}

	var rating sql.NullFloat64
	if err := db.QueryRow(
		"SELECT album_rating FROM spotify_reviews WHERE type = ? AND item_key = ?",
		"track", "spotify:track:1",
	).Scan(&rating); err != nil {
		t.Fatalf("failed to read stored review: %v", err)
	}

	if rating.Valid {
		t.Fatalf("expected no album rating for track review, got %v", rating.Float64)
	}
}

func sqlRating(value float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: value, Valid: true}
}

func createSpotifyReviewsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE spotify_reviews (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			item_key TEXT NOT NULL,
			review_url TEXT NOT NULL,
			review_text TEXT,
			album_rating REAL,
			created_at INTEGER DEFAULT (strftime('%s', 'now')),
			UNIQUE(type, item_key)
		);
	`)
	if err != nil {
		t.Fatalf("failed to create spotify_reviews table: %v", err)
	}
}
