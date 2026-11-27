package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/utils/facts"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestGetUserFactsPreservesLargeStructuredProfileAndAppearance(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)
	if err := chatmemory.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memory_migration_scopes(chat_id,state,updated_at) VALUES(100,'cutover',1)`); err != nil {
		t.Fatal(err)
	}
	insertUser(t, db, 1, "alice", "Alice", "One")
	insertMessage(t, db, 1, 100, 1, time.Now().Unix(), "hello")
	repo := chatmemory.NewRepository(db)
	var bodies []string
	for i := 0; i < 50; i++ {
		bodies = append(bodies, fmt.Sprintf("Independent profile detail marker %03d", i))
	}
	if err := repo.ReplacePersonFacts(context.Background(), 100, 1, bodies, "test"); err != nil {
		t.Fatal(err)
	}
	id := int64(1)
	for i := 0; i < 15; i++ {
		if _, _, err := repo.Add(context.Background(), chatmemory.Entry{ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &id, Body: fmt.Sprintf("Distinct pinned visual trait marker %03d", i), Retention: chatmemory.Pinned, SourceType: "stable_appearance"}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := NewGetUserFactsTool(db, 100).Execute(context.Background(), `{"users":["alice"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var payload getUserFactsResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Users) != 1 {
		t.Fatalf("unexpected result: %s", result)
	}
	item := payload.Users[0]
	if len(facts.ParseDossier(item.Facts).Identity) != 50 || len(item.Appearance) != 15 {
		t.Fatalf("facts or appearance clipped: facts=%d appearance=%d", len(facts.ParseDossier(item.Facts).Identity), len(item.Appearance))
	}
	for _, body := range bodies {
		if !strings.Contains(item.Facts, body) {
			t.Fatalf("fact missing: %s", body)
		}
	}
}
