package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultUserLimit = 50
	maxUserLimit     = 200
)

type FetchUsersTool struct {
	db     *sql.DB
	chatID int64
}

type fetchUsersArgs struct {
	Limit int `json:"limit"`
}

type fetchUsersResult struct {
	Users []string `json:"users"`
	Count int      `json:"count"`
}

func NewFetchUsersTool(db *sql.DB, chatID int64) *FetchUsersTool {
	return &FetchUsersTool{db: db, chatID: chatID}
}

func (t *FetchUsersTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "fetchUsers",
			Description: "Get users who have recently been active in this Telegram chat. Returns usernames or display names.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of users to return. Defaults to 50 and caps at 200.",
					},
				},
			},
		},
	}
}

func (t *FetchUsersTool) Execute(_ context.Context, args string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}

	parsedArgs := fetchUsersArgs{Limit: defaultUserLimit}
	if strings.TrimSpace(args) != "" {
		if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}

	if parsedArgs.Limit <= 0 {
		parsedArgs.Limit = defaultUserLimit
	}
	if parsedArgs.Limit > maxUserLimit {
		parsedArgs.Limit = maxUserLimit
	}

	users, err := fetchChatMembers(t.db, t.chatID, parsedArgs.Limit)
	if err != nil {
		return "", err
	}

	return marshalJSON(fetchUsersResult{Users: users, Count: len(users)}), nil
}

func fetchChatMembers(db *sql.DB, chatID int64, maxMembers int) ([]string, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(DISTINCT sender_id) FROM messages WHERE chat_id = ?`, chatID).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("counting chat members: %w", err)
	}
	if count == 0 {
		return nil, nil
	}

	query := `
		SELECT u.username, u.first_name, u.last_name, m.sender_id
		FROM messages m
		JOIN users u ON u.id = m.sender_id
		WHERE m.chat_id = ?
		GROUP BY m.sender_id
		ORDER BY MAX(m.unixtime) DESC`

	var rows *sql.Rows
	if count > maxMembers {
		query += " LIMIT ?"
		rows, err = db.Query(query, chatID, maxMembers)
	} else {
		rows, err = db.Query(query, chatID)
	}
	if err != nil {
		return nil, fmt.Errorf("retrieving chat members: %w", err)
	}
	defer rows.Close()

	members := make([]string, 0, min(count, maxMembers))
	for rows.Next() {
		var username, firstName, lastName sql.NullString
		var senderID int64
		if err := rows.Scan(&username, &firstName, &lastName, &senderID); err != nil {
			return nil, fmt.Errorf("scanning chat member: %w", err)
		}

		name := strings.TrimSpace(strings.Join([]string{firstName.String, lastName.String}, " "))
		switch {
		case username.Valid && username.String != "":
			members = append(members, username.String)
		case name != "":
			members = append(members, name)
		default:
			members = append(members, fmt.Sprintf("user_%d", senderID))
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating chat members: %w", err)
	}

	return members, nil
}
