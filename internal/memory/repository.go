package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	ChatLore     Kind = "chat_lore"
	PersonFact   Kind = "person_fact"
	PossiblePlan Kind = "possible_plan"
	Decision     Kind = "decision"
)

type Status string

const (
	Active     Status = "active"
	Completed  Status = "completed"
	Archived   Status = "archived"
	Superseded Status = "superseded"
)

type Entry struct {
	ID                 int64  `json:"id"`
	ChatID             int64  `json:"chat_id"`
	Kind               Kind   `json:"kind"`
	SubjectUserID      *int64 `json:"subject_user_id,omitempty"`
	Body               string `json:"body"`
	NormalizedBody     string `json:"-"`
	Status             Status `json:"status"`
	SourceType         string `json:"source_type"`
	SourceMessageID    *int64 `json:"source_message_id,omitempty"`
	SourceUserID       *int64 `json:"source_user_id,omitempty"`
	LegacyPromptID     *int64 `json:"legacy_prompt_id,omitempty"`
	LegacyPersonFactID *int64 `json:"legacy_person_fact_id,omitempty"`
	SupersedesID       *int64 `json:"supersedes_id,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type Filter struct {
	ChatID          int64
	Kind            Kind
	SubjectUserID   *int64
	IncludeInactive bool
}

type Repository struct {
	db *sql.DB
	mu sync.Mutex
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func EnsureSchema(db *sql.DB) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('chat_lore','person_fact','possible_plan','decision')),
			subject_user_id INTEGER,
			body TEXT NOT NULL,
			normalized_body TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','completed','archived','superseded')),
			source_type TEXT NOT NULL,
			source_message_id INTEGER,
			source_user_id INTEGER,
			legacy_prompt_id INTEGER,
			legacy_person_fact_id INTEGER,
			supersedes_id INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY(chat_id) REFERENCES chats(id),
			FOREIGN KEY(subject_user_id) REFERENCES users(id),
			FOREIGN KEY(supersedes_id) REFERENCES memory_entries(id)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_active_dedupe
			ON memory_entries(chat_id, kind, COALESCE(subject_user_id, 0), normalized_body)
			WHERE status = 'active';
		CREATE INDEX IF NOT EXISTS idx_memory_chat_kind_status
			ON memory_entries(chat_id, kind, status, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_memory_person
			ON memory_entries(chat_id, subject_user_id, status, updated_at DESC);
		CREATE INDEX IF NOT EXISTS idx_memory_source_message
			ON memory_entries(chat_id, source_message_id);
		CREATE TABLE IF NOT EXISTS memory_migrations (
			source_type TEXT NOT NULL,
			source_id INTEGER NOT NULL,
			source_item TEXT NOT NULL,
			entry_id INTEGER NOT NULL,
			migrated_at INTEGER NOT NULL,
			PRIMARY KEY(source_type, source_id, source_item),
			FOREIGN KEY(entry_id) REFERENCES memory_entries(id)
		);
		CREATE TABLE IF NOT EXISTS memory_legacy_snapshots (
			source_table TEXT NOT NULL,
			source_row_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			subject_user_id INTEGER,
			source_version INTEGER NOT NULL,
			raw_text TEXT NOT NULL,
			raw_sha256 TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(source_table, source_row_id)
		);
		CREATE TABLE IF NOT EXISTS memory_migration_scopes (
			chat_id INTEGER PRIMARY KEY,
			state TEXT NOT NULL CHECK(state IN ('legacy','ready','cutover','rolled_back')),
			updated_at INTEGER NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("ensure memory schema: %w", err)
	}
	return nil
}

func (r *Repository) Add(ctx context.Context, entry Entry) (Entry, bool, error) {
	if r == nil || r.db == nil {
		return Entry{}, false, errors.New("database is not initialized")
	}
	if err := prepareEntry(&entry); err != nil {
		return Entry{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.add(ctx, r.db, entry)
}

// AddTx adds an entry through a caller-owned transaction so memory and the
// state change that produced it can commit atomically.
func (r *Repository) AddTx(ctx context.Context, tx *sql.Tx, entry Entry) (Entry, bool, error) {
	if r == nil || r.db == nil || tx == nil {
		return Entry{}, false, errors.New("database and transaction are required")
	}
	if err := prepareEntry(&entry); err != nil {
		return Entry{}, false, err
	}
	return r.add(ctx, tx, entry)
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func (r *Repository) add(ctx context.Context, exec sqlExecutor, entry Entry) (Entry, bool, error) {
	result, err := exec.ExecContext(ctx, `
		INSERT OR IGNORE INTO memory_entries (
			chat_id, kind, subject_user_id, body, normalized_body, status,
			source_type, source_message_id, source_user_id, legacy_prompt_id,
			legacy_person_fact_id, supersedes_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ChatID, entry.Kind, nullableInt64(entry.SubjectUserID), entry.Body,
		entry.NormalizedBody, entry.Status, entry.SourceType,
		nullableInt64(entry.SourceMessageID), nullableInt64(entry.SourceUserID),
		nullableInt64(entry.LegacyPromptID), nullableInt64(entry.LegacyPersonFactID),
		nullableInt64(entry.SupersedesID), entry.CreatedAt, entry.UpdatedAt)
	if err != nil {
		return Entry{}, false, fmt.Errorf("add memory: %w", err)
	}
	changed, _ := result.RowsAffected()

	row := exec.QueryRowContext(ctx, `
		SELECT id, chat_id, kind, subject_user_id, body, normalized_body, status,
			source_type, source_message_id, source_user_id, legacy_prompt_id,
			legacy_person_fact_id, supersedes_id, created_at, updated_at
		FROM memory_entries
		WHERE chat_id=? AND kind=? AND COALESCE(subject_user_id,0)=COALESCE(?,0)
			AND normalized_body=? AND status='active'
		ORDER BY id DESC LIMIT 1`, entry.ChatID, entry.Kind, nullableInt64(entry.SubjectUserID), entry.NormalizedBody)
	stored, err := scanEntry(row)
	if err != nil {
		return Entry{}, false, fmt.Errorf("read added memory: %w", err)
	}
	return stored, changed > 0, nil
}

func (r *Repository) List(ctx context.Context, filter Filter) ([]Entry, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("database is not initialized")
	}
	query := `SELECT id, chat_id, kind, subject_user_id, body, normalized_body, status,
		source_type, source_message_id, source_user_id, legacy_prompt_id,
		legacy_person_fact_id, supersedes_id, created_at, updated_at
		FROM memory_entries WHERE chat_id=?`
	args := []interface{}{filter.ChatID}
	if filter.Kind != "" {
		query += ` AND kind=?`
		args = append(args, filter.Kind)
	}
	if filter.SubjectUserID != nil {
		query += ` AND subject_user_id=?`
		args = append(args, *filter.SubjectUserID)
	}
	if !filter.IncludeInactive {
		query += ` AND status='active'`
	}
	query += ` ORDER BY updated_at DESC, id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// ReplacePersonFacts atomically archives the current typed facts for one user
// and installs a new active snapshot while retaining history.
func (r *Repository) ReplacePersonFacts(ctx context.Context, chatID, userID int64, bodies []string, sourceType string) error {
	if r == nil || r.db == nil {
		return errors.New("database is not initialized")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.ReplacePersonFactsTx(ctx, tx, chatID, userID, bodies, sourceType); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplacePersonFactsTx replaces one person's active canonical fact snapshot in
// a caller-owned transaction.
func (r *Repository) ReplacePersonFactsTx(ctx context.Context, tx *sql.Tx, chatID, userID int64, bodies []string, sourceType string) error {
	if r == nil || r.db == nil || tx == nil {
		return errors.New("database and transaction are required")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_entries SET status='archived', updated_at=? WHERE chat_id=? AND kind='person_fact' AND subject_user_id=? AND status='active'`, time.Now().Unix(), chatID, userID); err != nil {
		return err
	}
	for _, body := range bodies {
		subject := userID
		entry := Entry{ChatID: chatID, Kind: PersonFact, SubjectUserID: &subject, Body: body, SourceType: sourceType}
		if err := prepareEntry(&entry); err != nil {
			return err
		}
		if _, _, err := r.add(ctx, tx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) SetStatus(ctx context.Context, id int64, status Status) error {
	if r == nil || r.db == nil {
		return errors.New("database is not initialized")
	}
	if status != Completed && status != Archived {
		return fmt.Errorf("unsupported terminal status %q", status)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE memory_entries SET status=?, updated_at=? WHERE id=? AND status='active' AND (? <> 'completed' OR kind IN ('possible_plan','decision'))`, status, time.Now().Unix(), id, status)
	if err != nil {
		return fmt.Errorf("update memory status: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return fmt.Errorf("active memory %d not found", id)
	}
	return nil
}

func (r *Repository) Supersede(ctx context.Context, oldID int64, replacement Entry) (Entry, error) {
	if r == nil || r.db == nil {
		return Entry{}, errors.New("database is not initialized")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := scanEntry(tx.QueryRowContext(ctx, `SELECT id, chat_id, kind, subject_user_id, body, normalized_body, status, source_type, source_message_id, source_user_id, legacy_prompt_id, legacy_person_fact_id, supersedes_id, created_at, updated_at FROM memory_entries WHERE id=?`, oldID))
	if err != nil {
		return Entry{}, fmt.Errorf("read superseded memory: %w", err)
	}
	if old.Status != Active {
		return Entry{}, fmt.Errorf("memory %d is not active", oldID)
	}
	if replacement.ChatID == 0 {
		replacement.ChatID = old.ChatID
	}
	if replacement.Kind == "" {
		replacement.Kind = old.Kind
	}
	if replacement.SubjectUserID == nil {
		replacement.SubjectUserID = old.SubjectUserID
	}
	if replacement.ChatID != old.ChatID || replacement.Kind != old.Kind || !sameSubject(replacement.SubjectUserID, old.SubjectUserID) {
		return Entry{}, errors.New("replacement must keep chat, kind, and subject")
	}
	replacement.SupersedesID = &old.ID
	if err := prepareEntry(&replacement); err != nil {
		return Entry{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_entries SET status='superseded', updated_at=? WHERE id=? AND status='active'`, time.Now().Unix(), oldID); err != nil {
		return Entry{}, err
	}
	created, _, err := r.add(ctx, tx, replacement)
	if err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, err
	}
	return created, nil
}

func prepareEntry(entry *Entry) error {
	entry.Body = cleanBody(entry.Body)
	entry.NormalizedBody = normalize(entry.Body)
	if entry.ChatID == 0 {
		return errors.New("chat_id is required")
	}
	if entry.Body == "" {
		return errors.New("memory body is required")
	}
	switch entry.Kind {
	case PersonFact:
		if entry.SubjectUserID == nil || *entry.SubjectUserID == 0 {
			return errors.New("person_fact requires subject_user_id")
		}
	case ChatLore, PossiblePlan, Decision:
		if entry.SubjectUserID != nil {
			return fmt.Errorf("%s must not have subject_user_id", entry.Kind)
		}
	default:
		return fmt.Errorf("unsupported memory kind %q", entry.Kind)
	}
	if entry.Status == "" {
		entry.Status = Active
	}
	if entry.Status != Active {
		return errors.New("new memories must be active")
	}
	entry.SourceType = strings.TrimSpace(entry.SourceType)
	if entry.SourceType == "" {
		return errors.New("source_type is required")
	}
	now := time.Now().Unix()
	if entry.CreatedAt == 0 {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt == 0 {
		entry.UpdatedAt = entry.CreatedAt
	}
	return nil
}

func cleanBody(body string) string {
	body = strings.TrimSpace(body)
	body = strings.TrimSpace(strings.TrimPrefix(body, "-"))
	return body
}

func normalize(body string) string { return strings.ToLower(strings.Join(strings.Fields(body), " ")) }
func nullableInt64(v *int64) interface{} {
	if v == nil {
		return nil
	}
	return *v
}
func sameSubject(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type rowScanner interface{ Scan(...interface{}) error }

func scanEntry(row rowScanner) (Entry, error) {
	var entry Entry
	var subject, sourceMessage, sourceUser, legacyPrompt, legacyPerson, supersedes sql.NullInt64
	err := row.Scan(&entry.ID, &entry.ChatID, &entry.Kind, &subject, &entry.Body, &entry.NormalizedBody,
		&entry.Status, &entry.SourceType, &sourceMessage, &sourceUser, &legacyPrompt,
		&legacyPerson, &supersedes, &entry.CreatedAt, &entry.UpdatedAt)
	if err != nil {
		return Entry{}, err
	}
	entry.SubjectUserID = nullPointer(subject)
	entry.SourceMessageID = nullPointer(sourceMessage)
	entry.SourceUserID = nullPointer(sourceUser)
	entry.LegacyPromptID = nullPointer(legacyPrompt)
	entry.LegacyPersonFactID = nullPointer(legacyPerson)
	entry.SupersedesID = nullPointer(supersedes)
	return entry, nil
}
func nullPointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}
