package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestTypedMemoryToolsRespectKindsAndChatScope(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	if err := chatmemory.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}

	planTool := NewAddPossiblePlanTool(db, 10)
	planRaw, err := planTool.Execute(context.Background(), `{"body":"Maybe visit Uji"}`)
	if err != nil {
		t.Fatal(err)
	}
	planID := memoryIDFromToolResult(t, planRaw)

	loreTool := NewRememberChatLoreTool(db, 10)
	loreRaw, err := loreTool.Execute(context.Background(), `{"body":"We call the bot Gooby"}`)
	if err != nil {
		t.Fatal(err)
	}
	loreID := memoryIDFromToolResult(t, loreRaw)

	complete := NewCompletePlanTool(db, 10)
	if _, err := complete.Execute(context.Background(), `{"id":`+jsonNumber(planID)+`}`); err != nil {
		t.Fatalf("complete possible plan: %v", err)
	}
	if _, err := complete.Execute(context.Background(), `{"id":`+jsonNumber(loreID)+`}`); err == nil {
		t.Fatal("completePlan accepted chat lore")
	}

	archiveOtherChat := NewArchiveMemoryTool(db, 11)
	if _, err := archiveOtherChat.Execute(context.Background(), `{"id":`+jsonNumber(loreID)+`}`); err == nil {
		t.Fatal("cross-chat memory mutation was accepted")
	}

	person := NewRememberPersonFactTool(db, 10)
	if _, err := person.Execute(context.Background(), `{"body":"Likes jazz"}`); err == nil {
		t.Fatal("person fact without subject_user_id was accepted")
	}
}

func TestSearchMemoriesToolFiltersByChatSubjectKindAndQuery(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)
	insertUser(t, db, 42, "focusshifter", "Victor", "")
	if err := chatmemory.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := chatmemory.NewRepository(db)
	subject := int64(42)
	for _, entry := range []chatmemory.Entry{
		{ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &subject, Body: "@focusshifter абсолютный василий", SourceType: "test"},
		{ChatID: 100, Kind: chatmemory.PersonFact, SubjectUserID: &subject, Body: "@focusshifter любит ML", SourceType: "test"},
		{ChatID: 100, Kind: chatmemory.ChatLore, Body: "Василий приносит чай", SourceType: "test"},
		{ChatID: 200, Kind: chatmemory.PersonFact, SubjectUserID: &subject, Body: "@focusshifter абсолютный василий", SourceType: "test"},
	} {
		if _, _, err := repo.Add(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewSearchMemoriesTool(db, 100)
	raw, err := tool.Execute(context.Background(), `{"query":"абсолютный василий","kind":"person_fact","subject_user_id":42}`)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Memories []chatmemory.Entry `json:"memories"`
		Count    int                `json:"count"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || len(payload.Memories) != 1 {
		t.Fatalf("expected one scoped match, got %s", raw)
	}
	if payload.Memories[0].ChatID != 100 || payload.Memories[0].Body != "@focusshifter абсолютный василий" {
		t.Fatalf("unexpected match: %#v", payload.Memories[0])
	}

	raw, err = tool.Execute(context.Background(), `{"query":"василий","subject_user_id":0}`)
	if err != nil {
		t.Fatal(err)
	}
	payload = struct {
		Memories []chatmemory.Entry `json:"memories"`
		Count    int                `json:"count"`
	}{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 2 {
		t.Fatalf("subject_user_id=0 must mean unscoped search, got %s", raw)
	}
}

func memoryIDFromToolResult(t *testing.T, raw string) int64 {
	t.Helper()
	var payload struct {
		Memory chatmemory.Entry `json:"memory"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Memory.ID == 0 {
		t.Fatalf("missing memory ID in %s", raw)
	}
	return payload.Memory.ID
}

func jsonNumber(value int64) string {
	return fmt.Sprintf("%d", value)
}
