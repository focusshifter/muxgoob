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
