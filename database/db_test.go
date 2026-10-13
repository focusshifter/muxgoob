package database

import (
	"database/sql"
	"testing"
)

func TestSpotifyReviewGeneratedModelMigration(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE spotify_reviews (type TEXT,item_key TEXT,review_text TEXT,review_url TEXT,album_rating REAL); INSERT INTO spotify_reviews VALUES ('album','historical','unchanged','https://telegra.ph/unchanged',8)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ensureSpotifyReviewGeneratedModel(db); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	var text, page string
	var model sql.NullString
	var score float64
	if err := db.QueryRow(`SELECT review_text,review_url,album_rating,generated_by_model FROM spotify_reviews`).Scan(&text, &page, &score, &model); err != nil {
		t.Fatal(err)
	}
	if text != "unchanged" || page != "https://telegra.ph/unchanged" || score != 8 || model.Valid {
		t.Fatalf("historical row modified: %q %q %v %+v", text, page, score, model)
	}
}
