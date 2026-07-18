package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestChatHistoryBoundsToolReturnsExactBounds(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	oldest := time.Date(2017, time.January, 2, 3, 4, 5, 0, time.UTC).Unix()
	latest := time.Date(2026, time.May, 11, 10, 0, 0, 0, time.UTC).Unix()
	insertMessage(t, db, 1, 100, 1, oldest, "oldest message")
	insertMessage(t, db, 2, 100, 1, latest, "latest message")
	insertMessage(t, db, 3, 200, 1, oldest-1, "other chat")

	result, err := NewChatHistoryBoundsTool(db, 100).Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatHistoryBoundsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Count != 2 || payload.EarliestTimestamp != oldest || payload.LatestTimestamp != latest {
		t.Fatalf("unexpected history bounds: %#v", payload)
	}
	if payload.EarliestRFC3339 != "2017-01-02T03:04:05Z" || payload.LatestRFC3339 != "2026-05-11T10:00:00Z" {
		t.Fatalf("unexpected formatted bounds: %#v", payload)
	}
}

func TestChatHistoryBoundsToolReturnsEmptyHistory(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	result, err := NewChatHistoryBoundsTool(db, 100).Execute(context.Background(), ``)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatHistoryBoundsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Count != 0 || payload.EarliestTimestamp != 0 || payload.LatestTimestamp != 0 {
		t.Fatalf("unexpected empty history bounds: %#v", payload)
	}
}
