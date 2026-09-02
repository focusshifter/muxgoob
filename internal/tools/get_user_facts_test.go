package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestGetUserFactsToolReturnsFactsForMultipleUsers(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	insertUser(t, db, 2, "", "Bob", "Two")
	insertPersonFacts(t, db, 100, 1, "likes Go", 1)
	insertPersonFacts(t, db, 100, 2, "likes Rust", 1)

	now := time.Now().Unix()
	insertMessage(t, db, 1, 100, 1, now-10, "hi")
	insertMessage(t, db, 2, 100, 2, now-5, "hello")

	tool := NewGetUserFactsTool(db, 100)
	result, err := tool.Execute(context.Background(), `{"users":["alice","Bob Two"]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count != 2 {
		t.Fatalf("expected count 2, got %d", payload.Count)
	}
	if payload.Users[0].UserID != 1 || payload.Users[0].Name != "alice" || payload.Users[0].Facts != "likes Go" {
		t.Fatalf("unexpected first result: %+v", payload.Users[0])
	}
	if payload.Users[1].UserID != 2 || payload.Users[1].Name != "Bob Two" || payload.Users[1].Facts != "likes Rust" {
		t.Fatalf("unexpected second result: %+v", payload.Users[1])
	}
}

func TestGetUserFactsToolDefinitionRequiresExactScopedLookupAndFactBinding(t *testing.T) {
	definition := NewGetUserFactsTool(nil, 100).Definition()
	if definition.Function == nil {
		t.Fatal("getUserFacts definition has no function")
	}
	for _, required := range []string{"only the specific users", "user_id", "never apply one user's facts to another"} {
		if !strings.Contains(definition.Function.Description, required) {
			t.Fatalf("getUserFacts description missing %q: %q", required, definition.Function.Description)
		}
	}
}

func TestGetUserFactsToolSeparatesMissingUsersAndUsersWithoutFacts(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "alice", "Alice", "One")
	insertUser(t, db, 2, "", "Bob", "Two")
	insertMessage(t, db, 1, 100, 1, time.Now().Unix(), "hi")
	insertMessage(t, db, 2, 100, 2, time.Now().Unix(), "hello")

	tool := NewGetUserFactsTool(db, 100)
	result, err := tool.Execute(context.Background(), `{"users":["alice","Bob Two","charlie"]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(payload.NotInChat) != 1 || payload.NotInChat[0] != "charlie" {
		t.Fatalf("expected charlie in not_in_chat, got %+v", payload.NotInChat)
	}
	if len(payload.NoFacts) != 2 {
		t.Fatalf("expected 2 no_facts entries, got %d", len(payload.NoFacts))
	}
}

func TestGetUserFactsToolResolvesQuotedProfileAlias(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "sasha_real", "Alexander", "")
	insertPersonFacts(t, db, 100, 1, "Identity:\n- Known in this chat as «Саня»\n- wears a hoodie", 1)
	insertMessage(t, db, 1, 100, 1, time.Now().Unix(), "hello")

	result, err := NewGetUserFactsTool(db, 100).Execute(context.Background(), `{"users":["Саня"]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Count != 1 || payload.Users[0].Name != "sasha_real" || !strings.Contains(payload.Users[0].Facts, "hoodie") {
		t.Fatalf("expected alias to resolve the participant facts, got %+v", payload)
	}
}

func TestGetUserFactsToolResolvesRussianDiminutiveFromLatinTelegramName(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "focusshifter", "Victor", "Shcherbakov")
	insertPersonFacts(t, db, 100, 1, "Identity:\n- has a van-dyke beard and glasses", 1)
	insertMessage(t, db, 1, 100, 1, time.Now().Unix(), "hello")

	result, err := NewGetUserFactsTool(db, 100).Execute(context.Background(), `{"users":["Витю"]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Count != 1 || payload.Users[0].Name != "focusshifter" || !strings.Contains(payload.Users[0].Facts, "van-dyke") {
		t.Fatalf("expected Витю to resolve focusshifter's facts, got %+v", payload)
	}
}

func TestGetUserFactsToolMatchesByUsernameOrName(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertUser(t, db, 1, "victor", "Victor", "Shcherbakov")
	insertPersonFacts(t, db, 100, 1, "likes shipping features", 1)
	insertMessage(t, db, 1, 100, 1, time.Now().Unix(), "hey")

	tool := NewGetUserFactsTool(db, 100)
	result, err := tool.Execute(context.Background(), `{"users":["victor","Victor Shcherbakov"]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Count != 2 {
		t.Fatalf("expected count 2, got %d", payload.Count)
	}
}
