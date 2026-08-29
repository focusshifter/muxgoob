package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MigrationCandidate struct {
	SourceType string `json:"source_type"`
	SourceID   int64  `json:"source_id"`
	SourceItem string `json:"source_item"`
	Entry      Entry  `json:"entry"`
}

type MigrationReport struct {
	Candidates     int                  `json:"candidates"`
	Snapshots      int                  `json:"snapshots,omitempty"`
	Applied        int                  `json:"applied"`
	Existing       int                  `json:"existing"`
	CutoverScopes  int                  `json:"cutover_scopes,omitempty"`
	Missing        []MigrationCandidate `json:"missing,omitempty"`
	SnapshotErrors []string             `json:"snapshot_errors,omitempty"`
	Items          []MigrationCandidate `json:"items,omitempty"`
}

type Migrator struct {
	db   *sql.DB
	repo *Repository
}

func NewMigrator(db *sql.DB) *Migrator { return &Migrator{db: db, repo: NewRepository(db)} }

func (m *Migrator) Plan(ctx context.Context, onlyChatID int64) (MigrationReport, error) {
	if m == nil || m.db == nil {
		return MigrationReport{}, fmt.Errorf("database is not initialized")
	}
	var candidates []MigrationCandidate
	promptQuery := `SELECT p.id,p.chat_id,p.prompt FROM prompts p JOIN (SELECT chat_id,MAX(version) version FROM prompts GROUP BY chat_id) latest ON latest.chat_id=p.chat_id AND latest.version=p.version WHERE p.chat_id<>0`
	args := []interface{}{}
	if onlyChatID != 0 {
		promptQuery += ` AND p.chat_id=?`
		args = append(args, onlyChatID)
	}
	rows, err := m.db.QueryContext(ctx, promptQuery, args...)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("read legacy prompts: %w", err)
	}
	for rows.Next() {
		var id, chatID int64
		var raw string
		if err := rows.Scan(&id, &chatID, &raw); err != nil {
			rows.Close()
			return MigrationReport{}, err
		}
		for i, body := range extractExplicitStableContext(raw) {
			legacy := id
			candidates = append(candidates, MigrationCandidate{SourceType: "prompt_stable_context", SourceID: id, SourceItem: fmt.Sprintf("stable:%d", i), Entry: Entry{ChatID: chatID, Kind: ChatLore, Body: body, SourceType: "legacy_prompt", LegacyPromptID: &legacy}})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MigrationReport{}, err
	}
	rows.Close()

	personQuery := `SELECT pf.id,pf.chat_id,pf.user_id,pf.facts FROM person_facts pf JOIN (SELECT chat_id,user_id,MAX(version) version FROM person_facts GROUP BY chat_id,user_id) latest ON latest.chat_id=pf.chat_id AND latest.user_id=pf.user_id AND latest.version=pf.version`
	args = nil
	if onlyChatID != 0 {
		personQuery += ` WHERE pf.chat_id=?`
		args = []interface{}{onlyChatID}
	}
	rows, err = m.db.QueryContext(ctx, personQuery, args...)
	if err != nil {
		return MigrationReport{}, fmt.Errorf("read legacy person facts: %w", err)
	}
	for rows.Next() {
		var id, chatID, userID int64
		var raw string
		if err := rows.Scan(&id, &chatID, &userID, &raw); err != nil {
			rows.Close()
			return MigrationReport{}, err
		}
		bullets := extractLegacyPersonFacts(raw)
		for i, body := range bullets {
			legacy := id
			subject := userID
			candidates = append(candidates, MigrationCandidate{SourceType: "person_facts", SourceID: id, SourceItem: fmt.Sprintf("fact:%d", i), Entry: Entry{ChatID: chatID, Kind: PersonFact, SubjectUserID: &subject, Body: body, SourceType: "legacy_person_facts", LegacyPersonFactID: &legacy}})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MigrationReport{}, err
	}
	rows.Close()
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Entry.ChatID != candidates[j].Entry.ChatID {
			return candidates[i].Entry.ChatID < candidates[j].Entry.ChatID
		}
		if candidates[i].SourceType != candidates[j].SourceType {
			return candidates[i].SourceType < candidates[j].SourceType
		}
		if candidates[i].SourceID != candidates[j].SourceID {
			return candidates[i].SourceID < candidates[j].SourceID
		}
		return candidates[i].SourceItem < candidates[j].SourceItem
	})
	return MigrationReport{Candidates: len(candidates), Items: candidates}, nil
}

func (m *Migrator) Apply(ctx context.Context, onlyChatID int64) (MigrationReport, error) {
	snapshots, err := m.snapshotLegacyRows(ctx, onlyChatID)
	if err != nil {
		return MigrationReport{}, err
	}
	report, err := m.Plan(ctx, onlyChatID)
	if err != nil {
		return report, err
	}
	report.Snapshots = snapshots
	groups := map[int64][]MigrationCandidate{}
	var chats []int64
	for _, candidate := range report.Items {
		if _, ok := groups[candidate.Entry.ChatID]; !ok {
			chats = append(chats, candidate.Entry.ChatID)
		}
		groups[candidate.Entry.ChatID] = append(groups[candidate.Entry.ChatID], candidate)
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i] < chats[j] })
	m.repo.mu.Lock()
	defer m.repo.mu.Unlock()
	for _, chatID := range chats {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return report, err
		}
		for _, candidate := range groups[chatID] {
			var existing int64
			err := tx.QueryRowContext(ctx, `SELECT entry_id FROM memory_migrations WHERE source_type=? AND source_id=? AND source_item=?`, candidate.SourceType, candidate.SourceID, candidate.SourceItem).Scan(&existing)
			if err == nil {
				report.Existing++
				continue
			}
			if err != sql.ErrNoRows {
				_ = tx.Rollback()
				return report, err
			}
			entry := candidate.Entry
			if err := prepareEntry(&entry); err != nil {
				_ = tx.Rollback()
				return report, err
			}
			stored, _, err := m.repo.add(ctx, tx, entry)
			if err != nil {
				_ = tx.Rollback()
				return report, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO memory_migrations(source_type,source_id,source_item,entry_id,migrated_at) VALUES(?,?,?,?,?)`, candidate.SourceType, candidate.SourceID, candidate.SourceItem, stored.ID, time.Now().Unix()); err != nil {
				_ = tx.Rollback()
				return report, err
			}
			report.Applied++
		}
		if err := tx.Commit(); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (m *Migrator) Verify(ctx context.Context, onlyChatID int64) (MigrationReport, error) {
	planned, err := m.Plan(ctx, onlyChatID)
	if err != nil {
		return planned, err
	}
	report := MigrationReport{Candidates: planned.Candidates}
	for _, candidate := range planned.Items {
		var chatID int64
		var kind Kind
		var subject sql.NullInt64
		var normalized string
		err := m.db.QueryRowContext(ctx, `SELECT e.chat_id,e.kind,e.subject_user_id,e.normalized_body FROM memory_migrations mm JOIN memory_entries e ON e.id=mm.entry_id WHERE mm.source_type=? AND mm.source_id=? AND mm.source_item=?`, candidate.SourceType, candidate.SourceID, candidate.SourceItem).Scan(&chatID, &kind, &subject, &normalized)
		expectedValid := candidate.Entry.SubjectUserID != nil
		expectedSubject := int64(0)
		if expectedValid {
			expectedSubject = *candidate.Entry.SubjectUserID
		}
		if err != nil || chatID != candidate.Entry.ChatID || kind != candidate.Entry.Kind || normalized != normalize(candidate.Entry.Body) || subject.Valid != expectedValid || (subject.Valid && subject.Int64 != expectedSubject) {
			report.Missing = append(report.Missing, candidate)
		} else {
			report.Existing++
		}
	}
	report.Snapshots, report.SnapshotErrors = m.verifySnapshots(ctx, onlyChatID)
	return report, nil
}

// Cutover marks verified chats as structured-memory readers without rewriting
// or deleting any legacy prompt/person-fact row.
func (m *Migrator) Cutover(ctx context.Context, onlyChatID int64) (int, error) {
	query := `UPDATE memory_migration_scopes SET state='cutover', updated_at=? WHERE state='ready'`
	args := []interface{}{time.Now().Unix()}
	if onlyChatID != 0 {
		query += ` AND chat_id=?`
		args = append(args, onlyChatID)
	}
	result, err := m.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

func (m *Migrator) MarkReady(ctx context.Context, onlyChatID int64) (int, error) {
	query := `SELECT DISTINCT chat_id FROM memory_legacy_snapshots WHERE chat_id<>0`
	args := []interface{}{}
	if onlyChatID != 0 {
		query += ` AND chat_id=?`
		args = append(args, onlyChatID)
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	var chats []int64
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			rows.Close()
			return 0, err
		}
		chats = append(chats, chatID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, chatID := range chats {
		if _, err := m.db.ExecContext(ctx, `INSERT INTO memory_migration_scopes(chat_id,state,updated_at) VALUES(?,'ready',?) ON CONFLICT(chat_id) DO UPDATE SET state='ready',updated_at=excluded.updated_at`, chatID, time.Now().Unix()); err != nil {
			return 0, err
		}
	}
	return len(chats), nil
}

func (m *Migrator) Rollback(ctx context.Context, chatID int64) error {
	if chatID == 0 {
		return fmt.Errorf("chat ID is required for rollback")
	}
	_, err := m.db.ExecContext(ctx, `UPDATE memory_migration_scopes SET state='rolled_back',updated_at=? WHERE chat_id=?`, time.Now().Unix(), chatID)
	return err
}

func IsCutover(ctx context.Context, db *sql.DB, chatID int64) bool {
	if db == nil || chatID == 0 {
		return false
	}
	var state string
	return db.QueryRowContext(ctx, `SELECT state FROM memory_migration_scopes WHERE chat_id=?`, chatID).Scan(&state) == nil && state == "cutover"
}

// StripLegacyStableContext removes only the explicitly headed legacy data
// section; it preserves all other prompt bytes and unknown headings.
func StripLegacyStableContext(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	inStable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "Stable context:") {
			inStable = true
			continue
		}
		if inStable && isLegacyHeading(trimmed) {
			inStable = false
		}
		if !inStable {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isLegacyHeading(trimmed string) bool {
	return strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ")
}

// HasLegacyStableContext reports whether a prompt explicitly contains the
// legacy Stable context section heading.
func HasLegacyStableContext(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "Stable context:") {
			return true
		}
	}
	return false
}

// ExtractLegacyStableContext returns the bullet bodies from an explicitly
// headed legacy Stable context section.
func ExtractLegacyStableContext(raw string) []string {
	return extractExplicitStableContext(raw)
}

func extractExplicitStableContext(raw string) []string {
	var result []string
	inStable := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "Stable context:") {
			inStable = true
			continue
		}
		if inStable && isLegacyHeading(trimmed) {
			inStable = false
			continue
		}
		if !inStable || trimmed == "" {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "-"), "*"))
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func extractLegacyPersonFacts(raw string) []string {
	var result []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasSuffix(trimmed, ":") {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "-"), "*"))
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 && strings.TrimSpace(raw) != "" {
		return []string{strings.TrimSpace(raw)}
	}
	return result
}

func rawHash(raw string) string { sum := sha256.Sum256([]byte(raw)); return hex.EncodeToString(sum[:]) }

func (m *Migrator) snapshotLegacyRows(ctx context.Context, onlyChatID int64) (int, error) {
	type source struct {
		table, query string
		person       bool
	}
	sources := []source{
		{"prompts", `SELECT id,chat_id,NULL,version,prompt FROM prompts`, false},
		{"person_facts", `SELECT id,chat_id,user_id,version,facts FROM person_facts`, true},
	}
	count := 0
	for _, src := range sources {
		query := src.query
		args := []interface{}{}
		if onlyChatID != 0 {
			query += ` WHERE chat_id=?`
			args = append(args, onlyChatID)
		}
		rows, err := m.db.QueryContext(ctx, query, args...)
		if err != nil {
			return count, err
		}
		type item struct {
			id, chatID, version int64
			subject             sql.NullInt64
			raw                 string
		}
		var items []item
		for rows.Next() {
			var item item
			if err := rows.Scan(&item.id, &item.chatID, &item.subject, &item.version, &item.raw); err != nil {
				rows.Close()
				return count, err
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return count, err
		}
		rows.Close()
		for _, item := range items {
			result, err := m.db.ExecContext(ctx, `INSERT OR IGNORE INTO memory_legacy_snapshots(source_table,source_row_id,chat_id,subject_user_id,source_version,raw_text,raw_sha256,created_at) VALUES(?,?,?,?,?,?,?,?)`, src.table, item.id, item.chatID, nullableNullInt64(item.subject), item.version, item.raw, rawHash(item.raw), time.Now().Unix())
			if err != nil {
				return count, err
			}
			changed, _ := result.RowsAffected()
			count += int(changed)
		}
	}
	return count, nil
}

func nullableNullInt64(value sql.NullInt64) interface{} {
	if !value.Valid {
		return nil
	}
	return value.Int64
}

func (m *Migrator) verifySnapshots(ctx context.Context, onlyChatID int64) (int, []string) {
	query := `SELECT source_table,source_row_id,raw_text,raw_sha256 FROM memory_legacy_snapshots`
	args := []interface{}{}
	if onlyChatID != 0 {
		query += ` WHERE chat_id=?`
		args = append(args, onlyChatID)
	}
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, []string{err.Error()}
	}
	count := 0
	var problems []string
	for rows.Next() {
		var table, raw, hash string
		var id int64
		if err := rows.Scan(&table, &id, &raw, &hash); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		count++
		if rawHash(raw) != hash {
			problems = append(problems, fmt.Sprintf("hash mismatch %s:%d", table, id))
		}
	}
	if err := rows.Err(); err != nil {
		problems = append(problems, err.Error())
	}
	rows.Close()
	expected := 0
	for _, table := range []string{"prompts", "person_facts"} {
		countQuery := `SELECT COUNT(*) FROM ` + table
		countArgs := []interface{}{}
		if onlyChatID != 0 {
			countQuery += ` WHERE chat_id=?`
			countArgs = append(countArgs, onlyChatID)
		}
		var tableCount int
		if err := m.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&tableCount); err != nil {
			problems = append(problems, err.Error())
		} else {
			expected += tableCount
		}
	}
	if count != expected {
		problems = append(problems, fmt.Sprintf("snapshot count mismatch: got %d want %d", count, expected))
	}
	return count, problems
}
