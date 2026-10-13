package spotify

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/focusshifter/muxgoob/database"
)

type reviewItemKey struct{ typ, key string }
type reviewItemLock struct {
	mu    sync.Mutex
	users int
}

var reviewItemLocks = struct {
	sync.Mutex
	items map[reviewItemKey]*reviewItemLock
}{items: make(map[reviewItemKey]*reviewItemLock)}

// Count waiters as well as holders so a key cannot get two live locks.
func lockReviewItem(typ, key string) func() {
	k := reviewItemKey{typ, key}
	reviewItemLocks.Lock()
	entry := reviewItemLocks.items[k]
	if entry == nil {
		entry = &reviewItemLock{}
		reviewItemLocks.items[k] = entry
	}
	entry.users++
	reviewItemLocks.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		reviewItemLocks.Lock()
		entry.users--
		if entry.users == 0 {
			delete(reviewItemLocks.items, k)
		}
		reviewItemLocks.Unlock()
	}
}

var errNoExistingReview = errors.New("no existing review")

type reviewPublication struct {
	text   string
	rating sql.NullFloat64
	model  string
}

// Dependencies keep the publication boundary testable without network calls.
type reviewPublicationOps struct {
	lookup   func(string, string) (string, error)
	generate func() (reviewPublication, error)
	publish  func(string, string) (string, error)
	save     func(string, string, string, reviewPublication) error
}

func runReviewPublication(typ, key string, regenerate bool, ops reviewPublicationOps) (string, error) {
	unlock := lockReviewItem(typ, key)
	defer unlock()
	existing, err := ops.lookup(typ, key)
	if err != nil {
		return "", err
	}
	if regenerate && existing == "" {
		return "", errNoExistingReview
	}
	if !regenerate && existing != "" {
		return existing, nil
	}
	review, err := ops.generate()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(review.text) == "" {
		return "", fmt.Errorf("empty generated review")
	}
	page, err := ops.publish(existing, review.text)
	if err != nil {
		return "", fmt.Errorf("failed to publish review; stored review left untouched: %w", err)
	}
	if strings.TrimSpace(page) == "" {
		return "", fmt.Errorf("publisher returned empty page URL; stored review left untouched")
	}
	if err := ops.save(typ, key, page, review); err != nil {
		return "", fmt.Errorf("review published at %s but database save failed; published page and stored provenance may differ: %w", page, err)
	}
	return page, nil
}

func lookupReviewURL(typ, key string) (string, error) {
	var page string
	err := database.DB.QueryRow(`SELECT review_url FROM spotify_reviews WHERE type=? AND item_key=?`, typ, key).Scan(&page)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to look up review: %w", err)
	}
	return strings.TrimSpace(page), nil
}

// A single statement commits URL, content, rating and provenance together.
func savePublishedReview(typ, key, page string, review reviewPublication) error {
	if typ != "album" {
		review.rating = sql.NullFloat64{}
	}
	model := sql.NullString{String: review.model, Valid: review.model != ""}
	_, err := database.DB.Exec(`INSERT INTO spotify_reviews
 (type,item_key,review_url,review_text,album_rating,generated_by_model)
 VALUES(?,?,?,?,?,?) ON CONFLICT(type,item_key) DO UPDATE SET
 review_url=excluded.review_url,review_text=excluded.review_text,
 album_rating=excluded.album_rating,generated_by_model=excluded.generated_by_model`,
		typ, key, page, review.text, review.rating, model)
	return err
}
