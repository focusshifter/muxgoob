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

func main() {
	var dbPath, backupPath string
	var chatID int64
	var apply, verify, rollback bool
	flag.StringVar(&dbPath, "db", "db/muxgoob.sqlite", "SQLite database path")
	flag.StringVar(&backupPath, "backup", "", "backup path for --apply (default: timestamped beside DB)")
	flag.Int64Var(&chatID, "chat", 0, "limit migration to one chat ID")
	flag.BoolVar(&apply, "apply", false, "apply the migration")
	flag.BoolVar(&verify, "verify", false, "verify an applied migration")
	flag.BoolVar(&rollback, "rollback", false, "return one chat to legacy reads without deleting migrated data")
	flag.Parse()
	if boolCount(apply, verify, rollback) > 1 {
		log.Fatal("choose only one of --apply, --verify, or --rollback")
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
	migrator := chatmemory.NewMigrator(db)
	ctx := context.Background()
	var report chatmemory.MigrationReport
	switch {
	case apply:
		if backupPath == "" {
			ext := filepath.Ext(dbPath)
			base := strings.TrimSuffix(dbPath, ext)
			backupPath = fmt.Sprintf("%s-pre-memory-v2-%s%s", base, time.Now().Format("20060102-150405"), ext)
		}
		if err := createBackup(db, backupPath); err != nil {
			log.Fatalf("create backup: %v", err)
		}
		report, err = migrator.Apply(ctx, chatID)
		if err == nil {
			verified, verifyErr := migrator.Verify(ctx, chatID)
			if verifyErr != nil {
				err = verifyErr
			} else if len(verified.Missing) > 0 {
				err = fmt.Errorf("verification failed: %d migrated items missing", len(verified.Missing))
			} else if len(verified.SnapshotErrors) > 0 {
				err = fmt.Errorf("snapshot verification failed: %s", strings.Join(verified.SnapshotErrors, "; "))
			} else {
				if _, err = migrator.MarkReady(ctx, chatID); err == nil {
					report.CutoverScopes, err = migrator.Cutover(ctx, chatID)
				}
			}
		}
		if err == nil {
			fmt.Fprintf(os.Stderr, "backup: %s\n", backupPath)
		}
	case verify:
		report, err = migrator.Verify(ctx, chatID)
	case rollback:
		err = migrator.Rollback(ctx, chatID)
		report = chatmemory.MigrationReport{}
	default:
		report, err = migrator.Plan(ctx, chatID)
	}
	if err != nil {
		log.Fatal(err)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(encoded))
	if verify && (len(report.Missing) > 0 || len(report.SnapshotErrors) > 0) {
		os.Exit(2)
	}
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
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
	escaped := strings.ReplaceAll(path, "'", "''")
	_, err := db.Exec(`VACUUM INTO '` + escaped + `'`)
	return err
}
