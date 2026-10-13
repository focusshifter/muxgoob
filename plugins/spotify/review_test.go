package spotify

import (
	"database/sql"
	"encoding/json"
	"math"
	"reflect"
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
		"азартный, придирчивый музыкальный критик",
		"авторскую рецензию",
		"mixed reception",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestBuildSpotifyGroundingQuery_UsesPublishedCriticism(t *testing.T) {
	query := buildSpotifyGroundingQuery("album", "Kontravoid", "Sound of the Void", "2026")
	for _, part := range []string{"album Kontravoid - Sound of the Void (2026)", "independent published music criticism", "critic's praise or criticism", "single reviews", "publication name"} {
		if !strings.Contains(query, part) {
			t.Fatalf("query lacks %q: %s", part, query)
		}
	}
	if strings.Contains(query, "only affirmative facts") || !strings.Contains(query, "Exclude artist/label promotion") {
		t.Fatalf("query replaced critics with a fact sheet or promotional copy: %s", query)
	}
	trackQuery := buildSpotifyGroundingQuery("track", "Kontravoid", "Without", "2026")
	if !strings.Contains(trackQuery, "track Kontravoid - Without (2026)") || strings.Contains(trackQuery, "singles from this album") {
		t.Fatalf("track query not focused on the track: %s", trackQuery)
	}
}

func TestSpotifyReviewGrounding_AddsSpotifyTrackNames(t *testing.T) {
	var album SpotifyAlbum
	if err := json.Unmarshal([]byte(`{"tracks":{"items":[{"name":"Without"},{"name":"Mortal"}],"next":"https://api.spotify.com/v1/next"}}`), &album); err != nil {
		t.Fatal(err)
	}
	got := spotifyReviewGrounding("Artoffact Records", &album)
	want := "Track titles from Spotify (first page): Without; Mortal\n\nArtoffact Records"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := spotifyReviewGrounding("", &album); !strings.Contains(got, "Without; Mortal") {
		t.Fatalf("track names lost when research fails: %q", got)
	}
	if got := spotifyReviewGrounding("research", nil); got != "research" {
		t.Fatalf("track reviews must not gain album tracks: %q", got)
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

func TestBuildSpotifyReviewPrompt_PreservesVoiceWithoutImposingStructure(t *testing.T) {
	old := registry.Config
	t.Cleanup(func() { registry.Config = old })
	registry.Config = registry.Configuration{SpotifyReviewPrompt: "Свободный голос: {artist} — {title} ({year}), {type}.\n{grounding}"}
	for _, typ := range []string{"album", "track"} {
		t.Run(typ, func(t *testing.T) {
			prompt := buildSpotifyReviewPrompt(typ, "Artist", "Title", "2007", "SUPPORTED MUSIC")
			prefix := "Свободный голос: Artist — Title (2007), " + typ + ".\nSUPPORTED MUSIC"
			if !strings.HasPrefix(prompt, prefix) {
				t.Fatalf("configured voice or grounding changed: %s", prompt)
			}
			suffix := strings.TrimPrefix(prompt, prefix)
			expected := "\n\nReturn the review in the structured review_text field and the numeric rating in album_rating."
			if typ != "album" {
				expected = "\n\nReturn the review in review_text and album_rating=null as structured fields."
			}
			expected += "\n\nDo not manufacture quotes, consensus, biographical events or musical facts."
			expected += "\n\nSpotify titles are metadata only: never infer sound from track names. Keep named-song sound descriptions tied to songs actually covered; do not invent sequencing, transitions or details of undocumented songs."
			if suffix != expected {
				t.Fatalf("unexpected extra instructions in runtime suffix: %q", suffix)
			}
			for _, imposed := range []string{"narrative arc", "Open on", "central aesthetic tension", "one central thesis", "one or two sharp", "one good joke", "2 compact paragraphs", "decisive conclusion", "close on", "Let the conclusion earn", "Length:", "word budget", "first-hand listening", "Evidence rules:", "partial coverage", "0.5 increments"} {
				if strings.Contains(suffix, imposed) {
					t.Errorf("runtime contract imposes prose structure %q", imposed)
				}
			}
			for _, required := range []string{"review_text", "Do not manufacture quotes, consensus, biographical events or musical facts.", "Spotify titles are metadata only: never infer sound from track names.", "Keep named-song sound descriptions tied to songs actually covered; do not invent sequencing, transitions or details of undocumented songs.", "album_rating"} {
				if !strings.Contains(suffix, required) {
					t.Errorf("runtime contract lost safeguard %q", required)
				}
			}
			if typ == "track" && !strings.Contains(suffix, "album_rating=null") {
				t.Error("track must retain null rating")
			}
		})
	}
}

func TestBuildTelegraphReviewContentStartsWithSpotifyLink(t *testing.T) {
	content := buildTelegraphReviewContent("https://open.spotify.com/album/album123", "Отличный альбом.")
	if len(content) != 2 {
		t.Fatalf("expected link and review paragraphs, got %#v", content)
	}
	if content[0].Tag != "p" || len(content[0].Children) != 1 {
		t.Fatalf("unexpected Spotify link paragraph: %#v", content[0])
	}
	link, ok := content[0].Children[0].(telegraphNode)
	if !ok {
		t.Fatalf("expected a Telegraph link node, got %#v", content[0].Children[0])
	}
	if link.Tag != "a" || link.Attrs["href"] != "https://open.spotify.com/album/album123" || !reflect.DeepEqual(link.Children, []interface{}{"Spotify"}) {
		t.Fatalf("unexpected Spotify link: %#v", link)
	}
	if !reflect.DeepEqual(content[1].Children, []interface{}{"Отличный альбом."}) {
		t.Fatalf("unexpected review paragraph: %#v", content[1])
	}
}

func TestBuildSpotifyReviewPrompt_AlbumRequiresRatingParagraph(t *testing.T) {
	registry.Config = registry.Configuration{
		SpotifyReviewPrompt: "Review {artist} - {title}. Facts: {grounding}",
	}

	prompt := buildSpotifyReviewPrompt("album", "Deer Hunter", "Sunya", "2025", "praised groove")

	checks := []string{
		"the numeric rating in album_rating.",
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

	if strings.Contains(prompt, "the numeric rating in album_rating") {
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
	if albumRating["type"] != "number" {
		t.Fatalf("expected numeric album_rating, got %v", albumRating)
	}
	for _, bound := range []string{"minimum", "maximum", "multipleOf", "enum"} {
		if _, exists := albumRating[bound]; exists {
			t.Errorf("unexpected rating restriction %s", bound)
		}
	}
}

func TestSpotifyReviewSchema_TrackRequiresNullRating(t *testing.T) {
	req := buildSpotifyReviewCompletionRequest("test-model", "prompt", "track")
	data, err := req.ResponseFormat.JSONSchema.Schema.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	rating := schema["properties"].(map[string]any)["album_rating"].(map[string]any)
	if rating["type"] != "null" {
		t.Fatalf("track schema permits numeric rating: %v", rating)
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

func TestParseSpotifyReviewCompletion_PreservesAnyFiniteRating(t *testing.T) {
	for _, tc := range []struct {
		value   float64
		display string
	}{
		{0, "0 / 10"}, {-2.75, "-2,75 / 10"}, {4.2, "4,2 / 10"}, {9.999, "9,999 / 10"}, {10.125, "10,125 / 10"}, {42, "42 / 10"},
	} {
		data, err := json.Marshal(map[string]any{"review_text": "Тестовая рецензия.", "album_rating": tc.value})
		if err != nil {
			t.Fatal(err)
		}
		body, rating, err := parseSpotifyReviewCompletion(string(data), "album")
		if err != nil || !rating.Valid || rating.Float64 != tc.value || body != "Тестовая рецензия.\n\n"+tc.display {
			t.Fatalf("rating %v rounded or rejected: %q %+v %v", tc.value, body, rating, err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if isValidAlbumRating(value) {
			t.Fatalf("non-finite value accepted: %v", value)
		}
	}
}

func TestSaveReviewText_PreservesUnrestrictedRatingsAndExistingRows(t *testing.T) {
	db := testutils.SetupTestDB(t)
	old := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = old; _ = db.Close() })
	createSpotifyReviewsTable(t, db)
	if err := saveReviewText("album", "old", "Original review", sqlRating(7.5)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{0, -2.75, 9.999, 10.125, 42} {
		body := formatAlbumRating(value)
		if err := saveReviewText("album", body, "New review", sqlRating(value)); err != nil {
			t.Fatal(err)
		}
		var stored float64
		if err := db.QueryRow("SELECT album_rating FROM spotify_reviews WHERE item_key=?", body).Scan(&stored); err != nil || stored != value {
			t.Fatalf("DB changed score %g to %g: %v", value, stored, err)
		}
	}
	var text string
	var score float64
	if err := db.QueryRow("SELECT review_text,album_rating FROM spotify_reviews WHERE item_key='old'").Scan(&text, &score); err != nil || text != "Original review" || score != 7.5 {
		t.Fatal("existing review changed", err)
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

func TestSaveReviewText_GeneratedModelInsertUpdate(t *testing.T) {
	db := testutils.SetupTestDB(t)
	old := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = old; db.Close() })
	createSpotifyReviewsTable(t, db)
	if err := saveGeneratedReviewText("album", "item", "first", sqlRating(8), testOpus); err != nil {
		t.Fatal(err)
	}
	if err := saveReviewURL("album", "item", "original-page"); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{testOpus, testGPT, "openai/gpt-5.6-sol"} {
		if err := saveGeneratedReviewText("album", "item", "explicit regeneration", sqlRating(3.75), model); err != nil {
			t.Fatal(err)
		}
		var stored, url string
		if err := db.QueryRow(`SELECT generated_by_model,review_url FROM spotify_reviews WHERE item_key='item'`).Scan(&stored, &url); err != nil {
			t.Fatal(err)
		}
		if stored != model || url != "original-page" {
			t.Fatalf("stored=%q url=%q", stored, url)
		}
	}
	if err := saveReviewText("track", "historical-compatible", "text", sql.NullFloat64{}); err != nil {
		t.Fatal(err)
	}
	var unknown sql.NullString
	if err := db.QueryRow(`SELECT generated_by_model FROM spotify_reviews WHERE item_key='historical-compatible'`).Scan(&unknown); err != nil || unknown.Valid {
		t.Fatalf("guessed model=%+v err=%v", unknown, err)
	}
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
			generated_by_model TEXT,
			created_at INTEGER DEFAULT (strftime('%s', 'now')),
			UNIQUE(type, item_key)
		);
	`)
	if err != nil {
		t.Fatalf("failed to create spotify_reviews table: %v", err)
	}
}
