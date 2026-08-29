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
