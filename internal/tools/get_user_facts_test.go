package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
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
	if !strings.Contains(result, `"appearance":[]`) {
		t.Fatalf("expected explicit empty appearance arrays, got %s", result)
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
	for _, required := range []string{"only the specific users", "user_id", "appearance", "never apply one user's facts to another"} {
		if !strings.Contains(definition.Function.Description, required) {
			t.Fatalf("getUserFacts description missing %q: %q", required, definition.Function.Description)
		}
	}
}

func TestGetUserFactsToolReturnsAppearanceSeparatelyFromNoisyIdentity(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)
	if err := chatmemory.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}

	insertUser(t, db, 1, "focusshifter", "Victor", "Shcherbakov")
	insertMessage(t, db, 1, 100, 1, time.Now().Unix(), "hello")
	if _, err := db.Exec(`INSERT INTO memory_migration_scopes (chat_id, state, updated_at) VALUES (100, 'cutover', ?)`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	repo := chatmemory.NewRepository(db)
	for i := 0; i < 20; i++ {
		subject := int64(1)
		if _, _, err := repo.Add(context.Background(), chatmemory.Entry{
			ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &subject,
			Body: "ordinary identity fact " + strconv.Itoa(i), SourceType: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	subject := int64(1)
	appearance := "Canonical depiction for focusshifter/Vityana: light-brown hair, a Van Dyke beard, and browline glasses."
	if _, _, err := repo.Add(context.Background(), chatmemory.Entry{
		ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &subject,
		Body: appearance, Retention: chatmemory.Pinned, SourceType: "stable_appearance",
	}); err != nil {
		t.Fatal(err)
	}
	catEars := "Canonical depiction for focusshifter/Vityana includes cat ears."
	if _, _, err := repo.Add(context.Background(), chatmemory.Entry{
		ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &subject,
		Body: catEars, Retention: chatmemory.Pinned, SourceType: "stable_appearance",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := NewGetUserFactsTool(db, 100).Execute(context.Background(), `{"users":["focusshifter"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Users) != 1 || len(payload.Users[0].Appearance) != 2 {
		t.Fatalf("expected an explicit authoritative appearance field, got %+v", payload)
	}
	joined := strings.Join(payload.Users[0].Appearance, "\n")
	if !strings.Contains(joined, appearance) || !strings.Contains(joined, catEars) {
		t.Fatalf("expected every alias-bound visual fact, got %+v", payload.Users[0].Appearance)
	}
	if strings.Contains(payload.Users[0].Facts, appearance) || strings.Contains(payload.Users[0].Facts, catEars) || strings.Contains(payload.Users[0].Facts, "Appearance:") {
		t.Fatalf("appearance leaked back into the general facts dossier: %q", payload.Users[0].Facts)
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
