package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestFetchUsersToolReturnsRecentMembers(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	insertUser(t, db, 2, "", "Bob", "Two")
	insertUser(t, db, 3, "", "", "")

	now := time.Now().Unix()
	insertMessage(t, db, 1, 100, 1, now-10, "hello")
	insertMessage(t, db, 2, 100, 2, now-5, "hi")
	insertMessage(t, db, 3, 100, 3, now-1, "yo")

	tool := NewFetchUsersTool(db, 100)
	result, err := tool.Execute(context.Background(), `{"limit":2}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload fetchUsersResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count != 2 {
		t.Fatalf("expected count 2, got %d", payload.Count)
	}

	expected := []string{"user_3", "Bob Two"}
	for i, want := range expected {
		if payload.Users[i] != want {
			t.Fatalf("expected user %d to be %q, got %q", i, want, payload.Users[i])
		}
	}
}
