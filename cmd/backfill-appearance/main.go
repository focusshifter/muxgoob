// backfill-appearance imports explicitly reviewed legacy visual facts as pinned
// structured memory. It deliberately does not classify arbitrary legacy prose.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
)

type manifest struct {
	SourceType string           `json:"source_type"`
	Facts      []appearanceFact `json:"facts"`
}

type appearanceFact struct {
	ChatID              int64  `json:"chat_id"`
	SubjectUserID       int64  `json:"subject_user_id"`
	LegacyPersonFactID  int64  `json:"legacy_person_fact_id"`
	LegacySubjectUserID int64  `json:"legacy_subject_user_id,omitempty"`
	SourceExcerpt       string `json:"source_excerpt,omitempty"`
	Body                string `json:"body"`
}

type result struct {
	Manifest string `json:"manifest"`
	Planned  int    `json:"planned"`
	Applied  int    `json:"applied"`
	Existing int    `json:"existing"`
}

func main() {
	var dbPath, manifestPath, backupPath string
	var apply bool
	flag.StringVar(&dbPath, "db", "db/muxgoob.sqlite", "SQLite database path")
	flag.StringVar(&manifestPath, "manifest", "", "reviewed appearance-backfill JSON manifest")
	flag.StringVar(&backupPath, "backup", "", "backup path for --apply (default: timestamped beside DB)")
	flag.BoolVar(&apply, "apply", false, "write pinned facts; without this flag only validate and report")
	flag.Parse()
	if strings.TrimSpace(manifestPath) == "" {
		log.Fatal("-manifest is required")
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		log.Fatal(err)
	}
	var input manifest
	if err := json.Unmarshal(data, &input); err != nil {
		log.Fatalf("parse manifest: %v", err)
	}
	if input.SourceType == "" {
		input.SourceType = "appearance_backfill"
	}
	if len(input.Facts) == 0 {
		log.Fatal("manifest has no facts")
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_busy_timeout=10000&_synchronous=NORMAL&cache=shared&_txlock=immediate&_foreign_keys=on")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
	if err := chatmemory.EnsureSchema(db); err != nil {
		log.Fatal(err)
	}
	if err := validateManifest(context.Background(), db, input); err != nil {
		log.Fatal(err)
	}

	report := result{Manifest: manifestPath, Planned: len(input.Facts)}
	if apply {
		if backupPath == "" {
			ext := filepath.Ext(dbPath)
			backupPath = fmt.Sprintf("%s-pre-appearance-backfill-%d%s", strings.TrimSuffix(dbPath, ext), time.Now().UnixNano(), ext)
		}
		if err := createBackup(db, backupPath); err != nil {
			log.Fatalf("create backup: %v", err)
		}
		repo := chatmemory.NewRepository(db)
		if err := withTx(context.Background(), db, func(tx *sql.Tx) error {
			for _, fact := range input.Facts {
				subject := fact.SubjectUserID
				entry := chatmemory.Entry{
					ChatID: fact.ChatID, Kind: chatmemory.PersonFact, SubjectUserID: &subject,
					Body: fact.Body, Retention: chatmemory.Pinned, SourceType: input.SourceType,
				}
				if fact.LegacyPersonFactID != 0 {
					legacy := fact.LegacyPersonFactID
					entry.LegacyPersonFactID = &legacy
				}
				_, changed, err := repo.AddTx(context.Background(), tx, entry)
				if err != nil {
					return err
				}
				if changed {
					report.Applied++
				} else {
					report.Existing++
				}
			}
			return nil
		}); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "backup: %s\n", backupPath)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
}

func validateManifest(ctx context.Context, db *sql.DB, input manifest) error {
	seen := map[string]struct{}{}
	for i, fact := range input.Facts {
		fact.Body = strings.TrimSpace(fact.Body)
		if fact.ChatID == 0 || fact.SubjectUserID == 0 || fact.Body == "" {
			return fmt.Errorf("fact %d requires chat_id, subject_user_id, and body", i)
		}
		if fact.LegacyPersonFactID == 0 && !strings.HasPrefix(input.SourceType, "owner_confirmed_") && !strings.HasPrefix(input.SourceType, "stable_alias_owner_confirmed_") {
			return fmt.Errorf("fact %d requires legacy_person_fact_id unless source_type is owner_confirmed_*", i)
		}
		key := fmt.Sprintf("%d/%d/%s", fact.ChatID, fact.SubjectUserID, strings.ToLower(fact.Body))
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate fact %d", i)
		}
		seen[key] = struct{}{}
		if fact.LegacyPersonFactID == 0 {
			continue
		}
		sourceUserID := fact.LegacySubjectUserID
		if sourceUserID == 0 {
			sourceUserID = fact.SubjectUserID
		}
		sourceExcerpt := strings.TrimSpace(fact.SourceExcerpt)
		if sourceExcerpt == "" {
			sourceExcerpt = fact.Body
		}
		var raw string
		err := db.QueryRowContext(ctx, `SELECT facts FROM person_facts WHERE id=? AND chat_id=? AND user_id=?`, fact.LegacyPersonFactID, fact.ChatID, sourceUserID).Scan(&raw)
		if err != nil {
			return fmt.Errorf("fact %d legacy source: %w", i, err)
		}
		if !strings.Contains(strings.ToLower(raw), strings.ToLower(sourceExcerpt)) {
			return fmt.Errorf("fact %d source_excerpt is not present verbatim in legacy source %d", i, fact.LegacyPersonFactID)
		}
	}
	return nil
}

func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func createBackup(db *sql.DB, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	_, err := db.Exec(`VACUUM INTO '` + strings.ReplaceAll(path, "'", "''") + `'`)
	return err
}
