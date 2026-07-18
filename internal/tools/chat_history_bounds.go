package tools

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// ChatHistoryBoundsTool returns the earliest and latest messages retained in a
// chat's local database. It does not query Telegram itself.
type ChatHistoryBoundsTool struct {
	db     *sql.DB
	chatID int64
}

type chatHistoryBoundsResult struct {
	Count             int    `json:"count"`
	EarliestTimestamp int64  `json:"earliest_timestamp,omitempty"`
	EarliestRFC3339   string `json:"earliest_rfc3339,omitempty"`
	LatestTimestamp   int64  `json:"latest_timestamp,omitempty"`
	LatestRFC3339     string `json:"latest_rfc3339,omitempty"`
}

func NewChatHistoryBoundsTool(db *sql.DB, chatID int64) *ChatHistoryBoundsTool {
	return &ChatHistoryBoundsTool{db: db, chatID: chatID}
}

func (t *ChatHistoryBoundsTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "getChatHistoryBounds",
			Description: "Get the exact earliest and latest message timestamps retained in this chat's local Gooby database, plus the total stored-message count. Use for questions about the first, oldest, earliest, or latest stored chat message.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

func (t *ChatHistoryBoundsTool) Execute(_ context.Context, _ string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}

	var result chatHistoryBoundsResult
	var earliest, latest sql.NullInt64
	if err := t.db.QueryRow(`
		SELECT COUNT(*), MIN(unixtime), MAX(unixtime)
		FROM messages
		WHERE chat_id = ?`, t.chatID).Scan(&result.Count, &earliest, &latest); err != nil {
		return "", fmt.Errorf("reading chat history bounds: %w", err)
	}
	if earliest.Valid {
		result.EarliestTimestamp = earliest.Int64
		result.EarliestRFC3339 = time.Unix(earliest.Int64, 0).UTC().Format(time.RFC3339)
	}
	if latest.Valid {
		result.LatestTimestamp = latest.Int64
		result.LatestRFC3339 = time.Unix(latest.Int64, 0).UTC().Format(time.RFC3339)
	}
	return marshalJSON(result), nil
}
