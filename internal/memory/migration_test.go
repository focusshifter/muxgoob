package memory

import (
	"context"
	"strings"
	"testing"
)

func TestStripLegacyStableContextPreservesUnknownSectionsAndColonBullets(t *testing.T) {
	raw := "Reply style:\n- concise\n\nStable context:\n- Ritual:\n- Kyoto\n\nCustom rules:\nkeep this verbatim\n\nAvoid:\n- spoilers"
	items := extractExplicitStableContext(raw)
	if len(items) != 2 || items[0] != "Ritual:" || items[1] != "Kyoto" {
		t.Fatalf("unexpected extracted stable context: %#v", items)
	}
	rendered := StripLegacyStableContext(raw)
	if strings.Contains(rendered, "Kyoto") || !strings.Contains(rendered, "Custom rules:\nkeep this verbatim") || !strings.Contains(rendered, "Avoid:") {
		t.Fatalf("unexpected lossless cutover rendering: %q", rendered)
	}
}

func TestMigratorPlanApplyVerifyIsLosslessAndIdempotent(t *testing.T) {
	db := setupRepositoryTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE prompts(id INTEGER PRIMARY KEY,chat_id INTEGER,version INTEGER,prompt TEXT,created_at INTEGER);
		CREATE TABLE person_facts(id INTEGER PRIMARY KEY,chat_id INTEGER,user_id INTEGER,facts TEXT,version INTEGER,created_at INTEGER);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO prompts VALUES(1,1,1,?,1)`, "Reply style:\n- old\n\nStable context:\n- old lore\n\nAvoid:\n- x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO prompts VALUES(2,1,2,?,2)`, "Reply style:\n- new\n\nStable context:\n- Kyoto plan\n- Chat joke\n\nAvoid:\n- x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO person_facts VALUES(3,1,10,?,1,1)`, "Identity:\n- Works in IT\n\nInterests:\n- Likes metal"); err != nil {
		t.Fatal(err)
	}
	m := NewMigrator(db)
	ctx := context.Background()
	plan, err := m.Plan(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Candidates != 4 {
		t.Fatalf("expected 4 latest legacy bullets, got %d: %#v", plan.Candidates, plan.Items)
	}
	applied, err := m.Apply(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Applied != 4 || applied.Existing != 0 {
		t.Fatalf("unexpected first apply: %#v", applied)
	}
	again, err := m.Apply(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if again.Applied != 0 || again.Existing != 4 {
		t.Fatalf("migration not idempotent: %#v", again)
	}
	verified, err := m.Verify(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.Missing) != 0 || len(verified.SnapshotErrors) != 0 || verified.Existing != 4 || verified.Snapshots != 3 {
		t.Fatalf("verification failed: %#v", verified)
	}
	if _, err := m.MarkReady(ctx, 0); err != nil {
		t.Fatal(err)
	}
	cutover, err := m.Cutover(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cutover != 1 || !IsCutover(ctx, db, 1) {
		t.Fatalf("expected one cutover scope, got %d", cutover)
	}
	var latestPrompt string
	if err := db.QueryRow(`SELECT prompt FROM prompts WHERE chat_id=1 ORDER BY version DESC LIMIT 1`).Scan(&latestPrompt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(latestPrompt, "Kyoto plan") {
		t.Fatalf("cutover modified legacy prompt: %q", latestPrompt)
	}
	if rendered := StripLegacyStableContext(latestPrompt); strings.Contains(rendered, "Kyoto plan") || !strings.Contains(rendered, "Reply style:") {
		t.Fatalf("runtime prompt cutover failed: %q", rendered)
	}
	var oldPromptCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM prompts WHERE chat_id=1 AND prompt LIKE '%Kyoto plan%'`).Scan(&oldPromptCount); err != nil {
		t.Fatal(err)
	}
	if oldPromptCount != 1 {
		t.Fatalf("expected untouched rollback prompt, got %d", oldPromptCount)
	}
	if err := m.Rollback(ctx, 1); err != nil || IsCutover(ctx, db, 1) {
		t.Fatalf("rollback failed: %v", err)
	}
	var oldLore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM memory_entries WHERE body='old lore'`).Scan(&oldLore); err != nil {
		t.Fatal(err)
	}
	if oldLore != 0 {
		t.Fatal("migrated a non-latest prompt")
	}
}
