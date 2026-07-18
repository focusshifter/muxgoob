package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestSearchMessagesToolReturnsRecentMatches(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	insertUser(t, db, 2, "", "Bob", "Two")

	now := time.Now().Unix()
	insertMessage(t, db, 1, 100, 1, now-10, "gooby says hi")
	insertMessage(t, db, 2, 100, 2, now-5, "nothing to see here")
	insertMessage(t, db, 3, 100, 2, now-1, "Gooby is online")
	insertMessage(t, db, 4, 100, 1, now-100000, "gooby from the past")

	tool := NewSearchMessagesTool(db, 100, 0)
	result, err := tool.Execute(context.Background(), `{"query":"gooby","limit":5}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count != 3 {
		t.Fatalf("expected count 3, got %d", payload.Count)
	}
	if payload.Results[0].Sender != "Bob Two" {
		t.Fatalf("expected latest sender Bob Two, got %q", payload.Results[0].Sender)
	}
	if payload.Results[1].Sender != "alice" {
		t.Fatalf("expected second sender alice, got %q", payload.Results[1].Sender)
	}
}

func TestSearchMessagesToolFiltersByDateRange(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	insertMessage(t, db, 1, 100, 1, time.Date(2017, time.January, 2, 12, 0, 0, 0, time.UTC).Unix(), "planning Japan")
	insertMessage(t, db, 2, 100, 1, time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC).Unix(), "planning Japan again")

	tool := NewSearchMessagesTool(db, 100, 0)
	result, err := tool.Execute(context.Background(), `{"query":"Japan","after":"2017-01-01","before":"2017-12-31"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Count != 1 || payload.Results[0].Timestamp != time.Date(2017, time.January, 2, 12, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("unexpected date-filtered results: %#v", payload)
	}
}

func TestSearchMessagesToolSortsOldestAcrossFullHistory(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "ivan", "Ivan", "")
	oldest := time.Date(2017, time.June, 5, 9, 0, 0, 0, time.UTC).Unix()
	insertMessage(t, db, 1, 100, 1, oldest, "аниме обсуждение в 2017")
	insertMessage(t, db, 2, 100, 1, time.Date(2026, time.June, 13, 9, 0, 0, 0, time.UTC).Unix(), "аниме обсуждение в 2026")

	result, err := NewSearchMessagesTool(db, 100, 0).Execute(context.Background(), `{"query":"аниме","sort":"oldest","limit":1}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Count != 1 || payload.Results[0].Timestamp != oldest {
		t.Fatalf("expected oldest match, got %#v", payload)
	}
}

func TestSearchMessagesToolRejectsInvalidDateRange(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	_, err := NewSearchMessagesTool(db, 100, 0).Execute(context.Background(), `{"query":"test","after":"2026-13-01"}`)
	if err == nil {
		t.Fatal("expected invalid date to be rejected")
	}
}

func TestSearchMessagesToolExpandsMechwarriorVariants(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	insertUser(t, db, 2, "bob", "Bob", "Two")

	now := time.Now().Unix()
	insertMessage(t, db, 1, 100, 1, now-30, "MechWarrior 5 is great")
	insertMessage(t, db, 2, 100, 2, now-20, "BattleTech lore owns")
	insertMessage(t, db, 3, 100, 2, now-10, "completely unrelated topic")

	tool := NewSearchMessagesTool(db, 100, 0)
	result, err := tool.Execute(context.Background(), `{"query":"мехвор","variants":["мехворриор","mechwarrior","mech warrior","battletech"],"limit":5}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count < 2 {
		t.Fatalf("expected at least 2 results, got %d", payload.Count)
	}

	texts := []string{payload.Results[0].Text, payload.Results[1].Text}
	joined := strings.ToLower(strings.Join(texts, "\n"))
	if !strings.Contains(joined, "mechwarrior") {
		t.Fatalf("expected mechwarrior result, got %q", joined)
	}
	if !strings.Contains(joined, "battletech") {
		t.Fatalf("expected battletech result, got %q", joined)
	}
}

func TestSearchMessagesToolRequiresQuery(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	tool := NewSearchMessagesTool(db, 100, 0)
	_, err := tool.Execute(context.Background(), `{"limit":5}`)
	if err == nil {
		t.Fatal("expected error for missing query")
	}
}

func TestSearchMessagesToolExcludesTriggeringMessage(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")

	now := time.Now().Unix()
	insertMessage(t, db, 10, 100, 1, now-20, "старое обсуждение активижн")
	insertMessage(t, db, 11, 100, 1, now-1, "обсуждались ли последние игры активижн")

	tool := NewSearchMessagesTool(db, 100, 11)
	result, err := tool.Execute(context.Background(), `{"query":"активижн","limit":5}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count != 1 {
		t.Fatalf("expected 1 result after excluding triggering message, got %d", payload.Count)
	}
	if strings.Contains(strings.ToLower(payload.Results[0].Text), "последние игры активижн") {
		t.Fatalf("expected triggering message to be excluded, got %q", payload.Results[0].Text)
	}
}

func TestSearchMessagesToolFTSFindsOlderExactPhraseBeyondRecentCandidateWindow(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")

	now := time.Now().Unix()
	insertMessage(t, db, 1, 100, 1, now-1000, "we should migrate this chat search to fts5 soon")
	for i := int64(0); i < 300; i++ {
		insertMessage(t, db, 1000+i, 100, 1, now-i, "chat search is noisy again")
	}

	tool := NewSearchMessagesTool(db, 100, 0)
	result, err := tool.Execute(context.Background(), `{"query":"migrate chat search to fts5","limit":3}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count == 0 {
		t.Fatal("expected at least one result")
	}
	if !strings.Contains(strings.ToLower(payload.Results[0].Text), "migrate this chat search to fts5") {
		t.Fatalf("expected older exact phrase match first, got %q", payload.Results[0].Text)
	}
}

func TestSearchMessagesToolFindsImageMetadataDescriptions(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	if _, err := db.Exec(`CREATE TABLE media_metadata (
		message_id INTEGER NOT NULL,
		chat_id INTEGER NOT NULL,
		media_type TEXT NOT NULL,
		file_id TEXT NOT NULL,
		model TEXT NOT NULL,
		description TEXT NOT NULL,
		visible_text TEXT,
		tags TEXT,
		status TEXT NOT NULL DEFAULT 'done',
		error TEXT,
		created_at INTEGER DEFAULT 0,
		updated_at INTEGER DEFAULT 0,
		PRIMARY KEY (chat_id, message_id, file_id)
	)`); err != nil {
		t.Fatalf("failed to create media_metadata: %v", err)
	}

	insertUser(t, db, 1, "alice", "Alice", "One")
	now := time.Now().Unix()
	insertMessage(t, db, 50, 100, 1, now-10, "")
	if _, err := db.Exec(`INSERT INTO media_metadata (message_id, chat_id, media_type, file_id, model, description, visible_text, tags, status) VALUES (?, ?, 'photo', 'file-cat', 'test-model', ?, '', 'кот,мем', 'done')`, 50, 100, "Шесть картинок пукающих котов, мемный альбом про виноватых котиков"); err != nil {
		t.Fatalf("failed to insert metadata: %v", err)
	}

	tool := NewSearchMessagesTool(db, 100, 0)
	result, err := tool.Execute(context.Background(), `{"query":"пукающих котов","limit":5}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload searchMessagesResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count != 1 {
		t.Fatalf("expected one image metadata result, got %d: %s", payload.Count, result)
	}
	if !strings.Contains(payload.Results[0].Text, "[image]") || !strings.Contains(payload.Results[0].Text, "пукающих котов") {
		t.Fatalf("expected image metadata in search result text, got %q", payload.Results[0].Text)
	}
}
