package tools

import (
	"context"
	"encoding/json"
	"testing"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"

	"github.com/focusshifter/muxgoob/utils/facts"
	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestRememberTopicToolAddsStableContextBullet(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertPrompt(t, db, 100, 1, facts.RenderChatPrompt(&facts.ChatPrompt{StableContext: []string{"old meme"}}))

	tool := NewRememberTopicTool(db, 100)
	result, err := tool.Execute(context.Background(), `{"topic":"эхочембер — recurring joke"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatTopicResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !payload.Changed || payload.Added != "эхочембер — recurring joke" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(payload.StableContext) != 2 {
		t.Fatalf("expected 2 stable context items, got %+v", payload.StableContext)
	}
	if payload.StableContext[0] != "эхочембер — recurring joke" {
		t.Fatalf("expected remembered topic first, got %+v", payload.StableContext)
	}
}

func TestRememberTopicToolBypassesStableContextBudget(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertPrompt(t, db, 100, 1, facts.RenderChatPrompt(&facts.ChatPrompt{StableContext: []string{"one", "two", "three", "four", "five", "six"}}))

	tool := NewRememberTopicTool(db, 100)
	result, err := tool.Execute(context.Background(), `{"topic":"seven"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatTopicResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(payload.StableContext) != 7 {
		t.Fatalf("expected 7 stable context items, got %+v", payload.StableContext)
	}
	if payload.StableContext[0] != "seven" {
		t.Fatalf("expected new topic preserved at front, got %+v", payload.StableContext)
	}
	if payload.StableContext[len(payload.StableContext)-1] != "six" {
		t.Fatalf("expected existing stable context to remain intact, got %+v", payload.StableContext)
	}
}

func TestForgetTopicToolRemovesMatchedBullet(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertPrompt(t, db, 100, 1, facts.RenderChatPrompt(&facts.ChatPrompt{StableContext: []string{"эхочембер is a recurring joke", "slay the spire 2"}}))

	tool := NewForgetTopicTool(db, 100)
	tool.matcher = func(ctx context.Context, chatID int64, stableContext []string, topic string) ([]string, error) {
		return []string{"эхочембер is a recurring joke"}, nil
	}

	result, err := tool.Execute(context.Background(), `{"topic":"эхочембер"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatTopicResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !payload.Changed {
		t.Fatalf("expected changed payload, got %+v", payload)
	}
	if len(payload.Removed) != 1 || payload.Removed[0] != "эхочембер is a recurring joke" {
		t.Fatalf("unexpected removed payload: %+v", payload)
	}
	if len(payload.StableContext) != 1 || payload.StableContext[0] != "slay the spire 2" {
		t.Fatalf("unexpected stable context after removal: %+v", payload.StableContext)
	}
}

func TestForgetTopicToolDoesNotRemoveReplyStyleBullet(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertPrompt(t, db, 100, 1, facts.RenderChatPrompt(&facts.ChatPrompt{
		ReplyStyle:    []string{"Deploy the established lexicon (эхочемберы, рейды)"},
		StableContext: []string{"slay the spire 2"},
	}))

	tool := NewForgetTopicTool(db, 100)
	tool.matcher = func(ctx context.Context, chatID int64, bullets []string, topic string) ([]string, error) {
		return []string{"Deploy the established lexicon (эхочемберы, рейды)"}, nil
	}

	result, err := tool.Execute(context.Background(), `{"topic":"эхочембер"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatTopicResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if payload.Changed || len(payload.Removed) != 0 {
		t.Fatalf("reply behavior must not be removed by a memory tool: %+v", payload)
	}
	currentPrompt, _, err := getLatestChatPrompt(db, 100)
	if err != nil {
		t.Fatalf("failed to load saved prompt: %v", err)
	}
	parsed := facts.ParseChatPrompt(currentPrompt)
	if len(parsed.ReplyStyle) != 1 {
		t.Fatalf("expected reply style bullet preserved, got %+v", parsed.ReplyStyle)
	}
}

func TestForgetTopicToolArchivesStructuredChatLore(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)
	insertPrompt(t, db, 100, 1, facts.RenderChatPrompt(&facts.ChatPrompt{}))

	remember := NewRememberTopicTool(db, 100)
	if _, err := remember.Execute(context.Background(), `{"topic":"Kyoto night walks"}`); err != nil {
		t.Fatal(err)
	}
	forget := NewForgetTopicTool(db, 100)
	forget.matcher = func(context.Context, int64, []string, string) ([]string, error) {
		return []string{"Kyoto night walks"}, nil
	}
	result, err := forget.Execute(context.Background(), `{"topic":"Kyoto"}`)
	if err != nil {
		t.Fatal(err)
	}
	var payload chatTopicResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Changed {
		t.Fatalf("expected structured memory removal: %+v", payload)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM memory_entries WHERE body='Kyoto night walks'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(chatmemory.Archived) {
		t.Fatalf("expected archived status, got %s", status)
	}
}

func TestForgetTopicToolNoopWhenMatcherFindsNothing(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	createToolTestTables(t, db)

	insertPrompt(t, db, 100, 1, facts.RenderChatPrompt(&facts.ChatPrompt{StableContext: []string{"echo chamber meme"}}))

	tool := NewForgetTopicTool(db, 100)
	tool.matcher = func(ctx context.Context, chatID int64, stableContext []string, topic string) ([]string, error) {
		return nil, nil
	}

	result, err := tool.Execute(context.Background(), `{"topic":"эхочембер"}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload chatTopicResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if payload.Changed {
		t.Fatalf("expected unchanged payload, got %+v", payload)
	}
}
