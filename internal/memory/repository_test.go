package memory

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func setupRepositoryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:memory_repo?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(`CREATE TABLE chats (id INTEGER PRIMARY KEY); CREATE TABLE users (id INTEGER PRIMARY KEY); INSERT INTO chats(id) VALUES (1); INSERT INTO users(id) VALUES (10);`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRepositoryAddDeduplicatesConcurrentEntries(t *testing.T) {
	db := setupRepositoryTestDB(t)
	defer db.Close()
	repo := NewRepository(db)
	entry := Entry{ChatID: 1, Kind: ChatLore, Body: "  Любит   Киото ", SourceType: "tool"}

	const workers = 8
	ids := make(chan int64, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := repo.Add(context.Background(), entry)
			if err != nil {
				errs <- err
				return
			}
			ids <- got.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("expected one id, got %d and %d", first, id)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memory_entries`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one row, got %d", count)
	}
}

func TestRepositoryValidatesKindsAndPersonSubject(t *testing.T) {
	db := setupRepositoryTestDB(t)
	defer db.Close()
	repo := NewRepository(db)
	cases := []Entry{
		{ChatID: 1, Kind: "unknown", Body: "x", SourceType: "tool"},
		{ChatID: 1, Kind: PersonFact, Body: "x", SourceType: "tool"},
		{ChatID: 1, Kind: ChatLore, SubjectUserID: int64Ptr(10), Body: "x", SourceType: "tool"},
	}
	for _, entry := range cases {
		if _, _, err := repo.Add(context.Background(), entry); err == nil {
			t.Fatalf("expected validation error for %#v", entry)
		}
	}
}

func TestRepositorySupersedePreservesHistory(t *testing.T) {
	db := setupRepositoryTestDB(t)
	defer db.Close()
	repo := NewRepository(db)
	old, _, err := repo.Add(context.Background(), Entry{ChatID: 1, Kind: Decision, Body: "Едем в мае", SourceType: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	newEntry, err := repo.Supersede(context.Background(), old.ID, Entry{ChatID: 1, Kind: Decision, Body: "Едем в июне", SourceType: "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if newEntry.SupersedesID == nil || *newEntry.SupersedesID != old.ID {
		t.Fatalf("missing supersedes link: %#v", newEntry)
	}
	entries, err := repo.List(context.Background(), Filter{ChatID: 1, IncludeInactive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected history of two entries, got %d", len(entries))
	}
	if entries[1].Status != Superseded {
		t.Fatalf("expected old entry superseded, got %#v", entries)
	}
}

func int64Ptr(v int64) *int64 { return &v }
