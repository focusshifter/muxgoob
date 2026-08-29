package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	openai "github.com/sashabaranov/go-openai"
)

type AddMemoryTool struct {
	db              *sql.DB
	chatID          int64
	kind            chatmemory.Kind
	name            string
	description     string
	sourceMessageID *int64
	sourceUserID    *int64
}

type addMemoryArgs struct {
	Body          string `json:"body"`
	SubjectUserID *int64 `json:"subject_user_id,omitempty"`
}

type ListMemoriesTool struct {
	db     *sql.DB
	chatID int64
}
type listMemoriesArgs struct {
	Kind            chatmemory.Kind `json:"kind,omitempty"`
	IncludeInactive bool            `json:"include_inactive,omitempty"`
}
type SearchMemoriesTool struct {
	db     *sql.DB
	chatID int64
}
type searchMemoriesArgs struct {
	Query           string          `json:"query"`
	Kind            chatmemory.Kind `json:"kind,omitempty"`
	SubjectUserID   *int64          `json:"subject_user_id,omitempty"`
	IncludeInactive bool            `json:"include_inactive,omitempty"`
	Limit           int             `json:"limit,omitempty"`
}
type MemoryStatusTool struct {
	db     *sql.DB
	chatID int64
	name   string
	status chatmemory.Status
	kind   chatmemory.Kind
}
type memoryIDArgs struct {
	ID int64 `json:"id"`
}
type SupersedeMemoryTool struct {
	db     *sql.DB
	chatID int64
}
type supersedeMemoryArgs struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

func NewRememberChatLoreTool(db *sql.DB, chatID int64) Tool {
	return newAddMemoryTool(db, chatID, chatmemory.ChatLore, "rememberChatLore", "Remember durable shared chat lore, traditions, persistent preferences, or standing rules.")
}
func NewRememberPersonFactTool(db *sql.DB, chatID int64) Tool {
	return newAddMemoryTool(db, chatID, chatmemory.PersonFact, "rememberPersonFact", "Remember a durable fact about one specific chat participant. Resolve the participant with fetchUsers and provide subject_user_id.")
}
func NewAddPossiblePlanTool(db *sql.DB, chatID int64) Tool {
	return newAddMemoryTool(db, chatID, chatmemory.PossiblePlan, "addPossiblePlan", "Save a possible place, purchase, trip, project, or other non-committed plan. Do not turn it into a schedule or decision.")
}
func NewRememberDecisionTool(db *sql.DB, chatID int64) Tool {
	return newAddMemoryTool(db, chatID, chatmemory.Decision, "rememberDecision", "Save a decision, commitment, or promise that has actually been agreed.")
}
func NewListMemoriesTool(db *sql.DB, chatID int64) Tool {
	return &ListMemoriesTool{db: db, chatID: chatID}
}
func NewSearchMemoriesTool(db *sql.DB, chatID int64) Tool {
	return &SearchMemoriesTool{db: db, chatID: chatID}
}
func NewCompletePlanTool(db *sql.DB, chatID int64) Tool {
	return &MemoryStatusTool{db: db, chatID: chatID, name: "completePlan", status: chatmemory.Completed, kind: chatmemory.PossiblePlan}
}
func NewArchiveMemoryTool(db *sql.DB, chatID int64) Tool {
	return &MemoryStatusTool{db: db, chatID: chatID, name: "archiveMemory", status: chatmemory.Archived}
}
func NewSupersedeMemoryTool(db *sql.DB, chatID int64) Tool {
	return &SupersedeMemoryTool{db: db, chatID: chatID}
}

func newAddMemoryTool(db *sql.DB, chatID int64, kind chatmemory.Kind, name, description string) *AddMemoryTool {
	return &AddMemoryTool{db: db, chatID: chatID, kind: kind, name: name, description: description}
}

func (t *AddMemoryTool) Definition() openai.Tool {
	properties := map[string]any{"body": map[string]any{"type": "string", "description": "One concise durable memory statement."}}
	required := []string{"body"}
	if t.kind == chatmemory.PersonFact {
		properties["subject_user_id"] = map[string]any{"type": "integer", "description": "Telegram user ID of the fact's subject."}
		required = append(required, "subject_user_id")
	}
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: t.name, Description: t.description, Parameters: map[string]any{"type": "object", "properties": properties, "required": required}}}
}
func (t *AddMemoryTool) Execute(ctx context.Context, raw string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	if err := chatmemory.EnsureSchema(t.db); err != nil {
		return "", err
	}
	var args addMemoryArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	entry, changed, err := chatmemory.NewRepository(t.db).Add(ctx, chatmemory.Entry{ChatID: t.chatID, Kind: t.kind, SubjectUserID: args.SubjectUserID, Body: args.Body, SourceType: "tool", SourceMessageID: t.sourceMessageID, SourceUserID: t.sourceUserID})
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"action": "remember", "changed": changed, "memory": entry}), nil
}

func (t *ListMemoriesTool) Definition() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "listMemories", Description: "List structured memories for this chat. Use to inspect lore, person facts, possible plans, or decisions before changing them.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"chat_lore", "person_fact", "possible_plan", "decision"}}, "include_inactive": map[string]any{"type": "boolean"}}}}}
}
func (t *ListMemoriesTool) Execute(ctx context.Context, raw string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	var args listMemoriesArgs
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	entries, err := chatmemory.NewRepository(t.db).List(ctx, chatmemory.Filter{ChatID: t.chatID, Kind: args.Kind, IncludeInactive: args.IncludeInactive})
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"memories": entries, "count": len(entries)}), nil
}

func (t *SearchMemoriesTool) Definition() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{
		Name:        "searchMemories",
		Description: "Search structured memories in this chat before archiving or superseding one. Scope person facts with subject_user_id when known.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":            map[string]any{"type": "string", "description": "Distinctive words from the memory to find."},
				"kind":             map[string]any{"type": "string", "enum": []string{"chat_lore", "person_fact", "possible_plan", "decision"}},
				"subject_user_id":  map[string]any{"type": "integer"},
				"include_inactive": map[string]any{"type": "boolean"},
				"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			},
			"required": []string{"query"},
		},
	}}
}

func (t *SearchMemoriesTool) Execute(ctx context.Context, raw string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	var args searchMemoriesArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	entries, err := chatmemory.NewRepository(t.db).List(ctx, chatmemory.Filter{
		ChatID:          t.chatID,
		Kind:            args.Kind,
		SubjectUserID:   args.SubjectUserID,
		IncludeInactive: args.IncludeInactive,
	})
	if err != nil {
		return "", err
	}
	tokens := memorySearchTokens(query)
	type scoredEntry struct {
		entry chatmemory.Entry
		score int
	}
	matches := make([]scoredEntry, 0)
	queryLower := strings.ToLower(query)
	for _, entry := range entries {
		bodyLower := strings.ToLower(entry.Body)
		score := 0
		if strings.Contains(bodyLower, queryLower) {
			score += 1000
		}
		for _, token := range tokens {
			if strings.Contains(bodyLower, token) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scoredEntry{entry: entry, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].entry.ID > matches[j].entry.ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]chatmemory.Entry, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.entry)
	}
	return marshalJSON(map[string]any{"memories": result, "count": len(result)}), nil
}

func memorySearchTokens(value string) []string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len([]rune(part)) >= 2 {
			result = append(result, part)
		}
	}
	return result
}

func (t *MemoryStatusTool) Definition() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: t.name, Description: "Change one active structured memory by its ID. List memories first when the ID is unknown.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}, "required": []string{"id"}}}}
}
func (t *MemoryStatusTool) Execute(ctx context.Context, raw string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	var args memoryIDArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	// Scope the mutation to this chat before updating.
	entries, err := chatmemory.NewRepository(t.db).List(ctx, chatmemory.Filter{ChatID: t.chatID, IncludeInactive: true})
	if err != nil {
		return "", err
	}
	var found *chatmemory.Entry
	for _, entry := range entries {
		if entry.ID == args.ID {
			copy := entry
			found = &copy
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("memory %d not found in this chat", args.ID)
	}
	if t.kind != "" && found.Kind != t.kind {
		return "", fmt.Errorf("memory %d is %s, not %s", args.ID, found.Kind, t.kind)
	}
	if err := chatmemory.NewRepository(t.db).SetStatus(ctx, args.ID, t.status); err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"id": args.ID, "status": t.status, "changed": true}), nil
}

func (t *SupersedeMemoryTool) Definition() openai.Tool {
	return openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "supersedeMemory", Description: "Replace an outdated memory while preserving its history. List memories first to obtain the ID.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}, "body": map[string]any{"type": "string"}}, "required": []string{"id", "body"}}}}
}
func (t *SupersedeMemoryTool) Execute(ctx context.Context, raw string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	var args supersedeMemoryArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	repo := chatmemory.NewRepository(t.db)
	entries, err := repo.List(ctx, chatmemory.Filter{ChatID: t.chatID, IncludeInactive: true})
	if err != nil {
		return "", err
	}
	var old *chatmemory.Entry
	for i := range entries {
		if entries[i].ID == args.ID {
			old = &entries[i]
			break
		}
	}
	if old == nil {
		return "", fmt.Errorf("memory %d not found in this chat", args.ID)
	}
	entry, err := repo.Supersede(ctx, args.ID, chatmemory.Entry{ChatID: t.chatID, Kind: old.Kind, SubjectUserID: old.SubjectUserID, Body: args.Body, SourceType: "tool"})
	if err != nil {
		return "", err
	}
	return marshalJSON(map[string]any{"action": "supersede", "changed": true, "memory": entry}), nil
}
