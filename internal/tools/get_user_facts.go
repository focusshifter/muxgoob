package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	factsutil "github.com/focusshifter/muxgoob/utils/facts"
	openai "github.com/sashabaranov/go-openai"
)

const maxUserFactsUsers = 25

type GetUserFactsTool struct {
	db     *sql.DB
	chatID int64
}

type getUserFactsArgs struct {
	Users []string `json:"users"`
}

type getUserFactsItem struct {
	Query string `json:"query"`
	Name  string `json:"name"`
	Facts string `json:"facts"`
}

type getUserFactsResult struct {
	Users     []getUserFactsItem `json:"users"`
	NotInChat []string           `json:"not_in_chat,omitempty"`
	NoFacts   []string           `json:"no_facts,omitempty"`
	Count     int                `json:"count"`
}

func NewGetUserFactsTool(db *sql.DB, chatID int64) *GetUserFactsTool {
	return &GetUserFactsTool{db: db, chatID: chatID}
}

func (t *GetUserFactsTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "getUserFacts",
			Description: "Get chat-scoped facts for one or more users in this Telegram chat. Pass @usernames, usernames, or display names from this chat.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"users": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "@usernames, usernames, or display names to look up in this chat. You can request multiple users in one call.",
					},
				},
				"required": []string{"users"},
			},
		},
	}
}

func (t *GetUserFactsTool) Execute(_ context.Context, args string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}

	parsedArgs := getUserFactsArgs{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	queries := make([]string, 0, len(parsedArgs.Users))
	seen := make(map[string]struct{}, len(parsedArgs.Users))
	for _, user := range parsedArgs.Users {
		normalized := normalizeSearchText(user)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		queries = append(queries, user)
	}

	if len(queries) == 0 {
		return "", fmt.Errorf("users is required")
	}
	if len(queries) > maxUserFactsUsers {
		queries = queries[:maxUserFactsUsers]
	}

	items, notInChat, noFacts, err := lookupUserFacts(t.db, t.chatID, queries)
	if err != nil {
		return "", err
	}

	return marshalJSON(getUserFactsResult{Users: items, NotInChat: notInChat, NoFacts: noFacts, Count: len(items)}), nil
}

func lookupUserFacts(db *sql.DB, chatID int64, queries []string) ([]getUserFactsItem, []string, []string, error) {
	results := make([]getUserFactsItem, 0, len(queries))
	notInChat := make([]string, 0)
	noFacts := make([]string, 0)

	for _, query := range queries {
		resolved, err := resolveChatUser(db, chatID, query)
		if err != nil {
			return nil, nil, nil, err
		}
		if resolved == nil {
			notInChat = append(notInChat, query)
			continue
		}

		facts, err := fetchLatestPersonFacts(db, chatID, resolved.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		if strings.TrimSpace(facts) == "" {
			noFacts = append(noFacts, query)
			continue
		}

		results = append(results, getUserFactsItem{
			Query: query,
			Name:  resolved.Name,
			Facts: facts,
		})
	}

	return results, notInChat, noFacts, nil
}

type resolvedChatUser struct {
	ID   int64
	Name string
}

var quotedUserAliasExp = regexp.MustCompile(`[«"]([^»"]{3,64})[»"]`)

func resolveChatUser(db *sql.DB, chatID int64, query string) (*resolvedChatUser, error) {
	normalized := normalizeSearchText(query)
	if normalized == "" {
		return nil, nil
	}

	rows, err := db.Query(`
		SELECT u.id, COALESCE(u.username, ''), COALESCE(u.first_name, ''), COALESCE(u.last_name, ''), MAX(m.unixtime) AS last_seen
		FROM users u
		JOIN messages m ON m.sender_id = u.id AND m.chat_id = ?
		GROUP BY u.id, u.username, u.first_name, u.last_name
		ORDER BY last_seen DESC`, chatID)
	if err != nil {
		return nil, fmt.Errorf("resolving user facts target: %w", err)
	}

	var chatUsers []resolvedChatUser
	for rows.Next() {
		var id int64
		var username, firstName, lastName string
		var lastSeen int64
		if err := rows.Scan(&id, &username, &firstName, &lastName, &lastSeen); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning user facts target: %w", err)
		}

		displayName := strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
		name := username
		if name == "" {
			name = displayName
		}
		if name == "" {
			name = fmt.Sprintf("user_%d", id)
		}
		resolved := resolvedChatUser{ID: id, Name: name}
		chatUsers = append(chatUsers, resolved)
		for _, candidate := range []string{username, displayName, firstName, lastName} {
			if normalizeSearchText(candidate) == normalized {
				rows.Close()
				return &resolved, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterating user facts targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("closing user facts targets: %w", err)
	}

	// A nickname may be recorded in a participant's durable facts rather than
	// Telegram profile. Resolve only explicitly quoted aliases, matching the
	// reply-context resolver, and keep the lookup scoped to this chat.
	for _, user := range chatUsers {
		facts, err := fetchLatestPersonFacts(db, chatID, user.ID)
		if err != nil {
			return nil, err
		}
		for _, match := range quotedUserAliasExp.FindAllStringSubmatch(facts, -1) {
			if len(match) > 1 && normalizeSearchText(match[1]) == normalized {
				resolved := user
				return &resolved, nil
			}
		}
	}
	return nil, nil
}

func fetchLatestPersonFacts(db *sql.DB, chatID, userID int64) (string, error) {
	if chatmemory.IsCutover(context.Background(), db, chatID) {
		entries, err := chatmemory.NewRepository(db).List(context.Background(), chatmemory.Filter{ChatID: chatID, Kind: chatmemory.PersonFact, SubjectUserID: &userID})
		if err != nil {
			return "", fmt.Errorf("retrieving structured person facts: %w", err)
		}
		dossier := &factsutil.Dossier{}
		for _, entry := range entries {
			if entry.Retention == chatmemory.Pinned {
				dossier.Appearance = append(dossier.Appearance, entry.Body)
			} else {
				dossier.Identity = append(dossier.Identity, entry.Body)
			}
		}
		return factsutil.RenderDossier(factsutil.EnforceDossierBudgets(dossier)), nil
	}
	var facts string
	err := db.QueryRow(`
		SELECT facts FROM person_facts
		WHERE chat_id = ? AND user_id = ?
		ORDER BY version DESC
		LIMIT 1`, chatID, userID).Scan(&facts)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("retrieving person facts: %w", err)
	}
	return factsutil.EnforcePersonFactsBudgets(facts), nil
}
