package spotify

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestReviewPublicationFailedEditPreservesTuple(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createSpotifyReviewsTable(t, db)
	_, err := db.Exec(`INSERT INTO spotify_reviews(type,item_key,review_url,review_text,album_rating,generated_by_model) VALUES('album','item','old-page','old-text',8,'old-model')`)
	if err != nil {
		t.Fatal(err)
	}
	editErr := errors.New("edit rejected")
	ops := reviewPublicationOps{
		lookup: func(typ, key string) (string, error) {
			var page string
			err := db.QueryRow(`SELECT review_url FROM spotify_reviews WHERE type=? AND item_key=?`, typ, key).Scan(&page)
			return page, err
		},
		generate: func() (reviewPublication, error) {
			return reviewPublication{"new-text", sqlRating(2), "new-model"}, nil
		},
		publish: func(page, text string) (string, error) { return "", editErr },
		save: func(typ, key, page string, r reviewPublication) error {
			_, err := db.Exec(`UPDATE spotify_reviews SET review_url=?,review_text=?,album_rating=?,generated_by_model=? WHERE type=? AND item_key=?`, page, r.text, r.rating, r.model, typ, key)
			return err
		},
	}
	page, err := runReviewPublication("album", "item", true, ops)
	if page != "" || !errors.Is(err, editErr) {
		t.Fatalf("page=%q err=%v", page, err)
	}
	var url, text, model string
	var rating float64
	if err := db.QueryRow(`SELECT review_url,review_text,album_rating,generated_by_model FROM spotify_reviews WHERE item_key='item'`).Scan(&url, &text, &rating, &model); err != nil {
		t.Fatal(err)
	}
	if url != "old-page" || text != "old-text" || rating != 8 || model != "old-model" {
		t.Fatalf("failed edit changed published tuple: %q %q %v %q", url, text, rating, model)
	}
}

func TestReviewPublicationSaveFailureReportsPublishedPage(t *testing.T) {
	saveErr := errors.New("disk full")
	published := false
	ops := reviewPublicationOps{
		lookup:   func(string, string) (string, error) { return "old-page", nil },
		generate: func() (reviewPublication, error) { return reviewPublication{text: "new", model: "new-model"}, nil },
		publish:  func(page, text string) (string, error) { published = true; return page, nil },
		save:     func(string, string, string, reviewPublication) error { return saveErr },
	}
	page, err := runReviewPublication("album", "save-failure", true, ops)
	if !published || page != "" || !errors.Is(err, saveErr) || !strings.Contains(err.Error(), "old-page") || !strings.Contains(err.Error(), "published") {
		t.Fatalf("published=%v page=%q err=%v", published, page, err)
	}
}

func TestReviewPublicationSerializesWholeWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		firstRegenerate, secondRegenerate bool
	}{
		{"create-create", false, false}, {"create-regenerate", false, true}, {"regenerate-regenerate", true, true}, {"regenerate-create", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var stored reviewPublication
			var storedPage, publishedText string
			if tc.firstRegenerate {
				storedPage = "original-page"
			}
			var generations, publications int
			entered := make(chan struct{})
			release := make(chan struct{})
			lookupSecond := make(chan struct{}, 1)
			ops := reviewPublicationOps{
				lookup: func(string, string) (string, error) { mu.Lock(); defer mu.Unlock(); return storedPage, nil },
				generate: func() (reviewPublication, error) {
					mu.Lock()
					generations++
					n := generations
					mu.Unlock()
					if n == 1 {
						close(entered)
						<-release
					}
					return reviewPublication{text: map[int]string{1: "first", 2: "second"}[n], model: map[int]string{1: "model-one", 2: "model-two"}[n]}, nil
				},
				publish: func(page, text string) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					publications++
					publishedText = text
					return "page", nil
				},
				save: func(typ, key, page string, r reviewPublication) error {
					mu.Lock()
					defer mu.Unlock()
					storedPage = page
					stored = r
					return nil
				},
			}
			done := make(chan error, 2)
			go func() { _, err := runReviewPublication("album", t.Name(), tc.firstRegenerate, ops); done <- err }()
			<-entered
			second := ops
			second.lookup = func(typ, key string) (string, error) { lookupSecond <- struct{}{}; return ops.lookup(typ, key) }
			go func() { _, err := runReviewPublication("album", t.Name(), tc.secondRegenerate, second); done <- err }()
			select {
			case <-lookupSecond:
				t.Error("second existence check ran before first workflow completed")
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			for range 2 {
				if err := <-done; err != nil {
					t.Error(err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			wantCount := 1
			wantText := "first"
			wantModel := "model-one"
			if tc.secondRegenerate {
				wantCount = 2
				wantText = "second"
				wantModel = "model-two"
			}
			if generations != wantCount || publications != wantCount || storedPage != "page" || stored.text != wantText || publishedText != wantText || stored.model != wantModel {
				t.Fatalf("generations=%d publications=%d page=%q stored=%+v published=%q", generations, publications, storedPage, stored, publishedText)
			}
		})
	}
}

func TestReviewPublicationDifferentItemsDoNotBlock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	ops := reviewPublicationOps{
		lookup:   func(string, string) (string, error) { return "", nil },
		generate: func() (reviewPublication, error) { return reviewPublication{text: "review"}, nil },
		publish:  func(string, string) (string, error) { return "page", nil },
		save:     func(string, string, string, reviewPublication) error { return nil },
	}
	blocked := ops
	blocked.generate = func() (reviewPublication, error) {
		close(entered)
		<-release
		return reviewPublication{text: "review"}, nil
	}
	first := make(chan error, 1)
	go func() { _, err := runReviewPublication("album", "blocked", false, blocked); first <- err }()
	<-entered
	done := make(chan error, 1)
	go func() { _, err := runReviewPublication("track", "blocked", false, ops); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("different type/item blocked")
	}
	close(release)
	if err := <-first; err != nil {
		t.Error(err)
	}
}

func TestPublishedReviewTupleSave(t *testing.T) {
	db := testutils.SetupTestDB(t)
	old := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = old; db.Close() })
	createSpotifyReviewsTable(t, db)
	for _, typ := range []string{"album", "track"} {
		for _, r := range []reviewPublication{{"old", sqlRating(8), "old-model"}, {"new", sqlRating(3.75), "openai/gpt-5.6-sol"}} {
			if err := savePublishedReview(typ, "item", "page-"+r.text, r); err != nil {
				t.Fatal(err)
			}
			var page, text, model string
			var rating sql.NullFloat64
			if err := db.QueryRow(`SELECT review_url,review_text,album_rating,generated_by_model FROM spotify_reviews WHERE type=? AND item_key='item'`, typ).Scan(&page, &text, &rating, &model); err != nil {
				t.Fatal(err)
			}
			wantRating := r.rating
			if typ == "track" {
				wantRating = sql.NullFloat64{}
			}
			if page != "page-"+r.text || text != r.text || model != r.model || rating != wantRating {
				t.Fatalf("inconsistent tuple: %q %q %q %+v", page, text, model, rating)
			}
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_review_update BEFORE UPDATE ON spotify_reviews BEGIN SELECT RAISE(ABORT,'save rejected'); END`); err != nil {
		t.Fatal(err)
	}
	edited := false
	_, err := runReviewPublication("album", "item", true, reviewPublicationOps{
		lookup: lookupReviewURL,
		generate: func() (reviewPublication, error) {
			return reviewPublication{"unsaved", sqlRating(1), "unsaved-model"}, nil
		},
		publish: func(page, text string) (string, error) { edited = true; return page, nil },
		save:    savePublishedReview,
	})
	if !edited || err == nil || !strings.Contains(err.Error(), "published at page-new but database save failed") {
		t.Fatalf("edited=%v error=%v", edited, err)
	}
	var text, model string
	if err := db.QueryRow(`SELECT review_text,generated_by_model FROM spotify_reviews WHERE type='album' AND item_key='item'`).Scan(&text, &model); err != nil {
		t.Fatal(err)
	}
	if text != "new" || model != "openai/gpt-5.6-sol" {
		t.Fatalf("failed save partially changed tuple: %q %q", text, model)
	}
}

func TestReviewPublicationAbortsBeforeSideEffects(t *testing.T) {
	for _, stage := range []string{"lookup", "generate", "empty", "publish"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New(stage)
			published, saved := false, false
			ops := reviewPublicationOps{
				lookup: func(string, string) (string, error) {
					if stage == "lookup" {
						return "", failure
					}
					return "", nil
				},
				generate: func() (reviewPublication, error) {
					if stage == "generate" {
						return reviewPublication{}, failure
					}
					if stage == "empty" {
						return reviewPublication{text: " "}, nil
					}
					return reviewPublication{text: "text"}, nil
				},
				publish: func(string, string) (string, error) { published = true; return "", failure },
				save:    func(string, string, string, reviewPublication) error { saved = true; return nil },
			}
			page, err := runReviewPublication("album", stage, false, ops)
			if err == nil || page != "" || saved || published != (stage == "publish") {
				t.Fatalf("page=%q err=%v published=%v saved=%v", page, err, published, saved)
			}
		})
	}
}
