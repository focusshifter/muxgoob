package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultMessageLimit    = 20
	maxMessageLimit        = 50
	searchCandidateLimit   = 250
	searchSubstringPenalty = 3
)

var querySplitPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

var searchStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "be": {}, "did": {}, "do": {}, "does": {}, "for": {}, "how": {},
	"if": {}, "in": {}, "is": {}, "it": {}, "of": {}, "on": {}, "or": {}, "the": {}, "to": {}, "was": {},
	"were": {}, "what": {}, "when": {}, "where": {}, "who": {}, "with": {},
	"а": {}, "в": {}, "во": {}, "где": {}, "да": {}, "же": {}, "и": {}, "или": {}, "к": {}, "как": {},
	"кто": {}, "ли": {}, "на": {}, "не": {}, "но": {}, "о": {}, "об": {}, "обо": {}, "по": {}, "про": {},
	"с": {}, "со": {}, "то": {}, "у": {}, "что": {}, "это": {},
}

type SearchMessagesTool struct {
	db               *sql.DB
	chatID           int64
	excludeMessageID int
}

type searchMessagesArgs struct {
	Query    string   `json:"query"`
	Variants []string `json:"variants"`
	Limit    int      `json:"limit"`
}

type searchMessageResult struct {
	Sender    string `json:"sender"`
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

type searchMessagesResult struct {
	Query   string                `json:"query"`
	Results []searchMessageResult `json:"results"`
	Count   int                   `json:"count"`
}

type rankedSearchMessageResult struct {
	searchMessageResult
	score int
}

func NewSearchMessagesTool(db *sql.DB, chatID int64, excludeMessageID int) *SearchMessagesTool {
	return &SearchMessagesTool{db: db, chatID: chatID, excludeMessageID: excludeMessageID}
}

func (t *SearchMessagesTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "searchMessages",
			Description: "Search chat messages by topic, keyword, or phrase. You can provide multiple spelling, transliteration, or franchise variants for the same topic in one call.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Main word, phrase, or topic to search for in chat history. Use the exact term when possible.",
					},
					"variants": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "string",
						},
						"description": "Optional alternate spellings, transliterations, spacing variants, aliases, or related franchise terms to search together with the main query. Generate these yourself when needed.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of results to return. Defaults to 20 and caps at 50.",
					},
				},
				"required": []string{"query"},
			},
		},
	}
}

func (t *SearchMessagesTool) Execute(_ context.Context, args string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}

	parsedArgs := searchMessagesArgs{Limit: defaultMessageLimit}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	parsedArgs.Query = strings.TrimSpace(parsedArgs.Query)
	if parsedArgs.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	cleanVariants := make([]string, 0, len(parsedArgs.Variants))
	for _, variant := range parsedArgs.Variants {
		variant = strings.TrimSpace(variant)
		if variant != "" {
			cleanVariants = append(cleanVariants, variant)
		}
	}
	parsedArgs.Variants = cleanVariants

	if parsedArgs.Limit <= 0 {
		parsedArgs.Limit = defaultMessageLimit
	}
	if parsedArgs.Limit > maxMessageLimit {
		parsedArgs.Limit = maxMessageLimit
	}

	results, err := searchMessages(t.db, t.chatID, t.excludeMessageID, parsedArgs.Query, parsedArgs.Variants, parsedArgs.Limit)
	if err != nil {
		return "", err
	}

	return marshalJSON(searchMessagesResult{
		Query:   parsedArgs.Query,
		Results: results,
		Count:   len(results),
	}), nil
}

func searchMessages(db *sql.DB, chatID int64, excludeMessageID int, query string, variants []string, limit int) ([]searchMessageResult, error) {
	patterns := buildSearchPatterns(query, variants)
	if len(patterns) == 0 {
		return nil, nil
	}

	whereParts := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns)+3)
	args = append(args, chatID)
	if excludeMessageID > 0 {
		args = append(args, excludeMessageID)
	}
	for _, pattern := range patterns {
		whereParts = append(whereParts, "LOWER(COALESCE(NULLIF(m.text, ''), m.caption, '')) LIKE ?")
		args = append(args, "%"+pattern+"%")
	}
	args = append(args, searchCandidateLimit)

	queryFilter := "WHERE m.chat_id = ?"
	if excludeMessageID > 0 {
		queryFilter += " AND m.id != ?"
	}

	rows, err := db.Query(`
		SELECT u.username, u.first_name, u.last_name, m.sender_id, m.unixtime, COALESCE(NULLIF(m.text, ''), m.caption, '')
		FROM messages m
		LEFT JOIN users u ON u.id = m.sender_id
		`+queryFilter+`
		  AND (`+strings.Join(whereParts, " OR ")+`)
		ORDER BY m.unixtime DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages: %w", err)
	}
	defer rows.Close()

	ranked := make([]rankedSearchMessageResult, 0, limit)
	for rows.Next() {
		var username, firstName, lastName sql.NullString
		var senderID int64
		var unixtime int64
		var text string
		if err := rows.Scan(&username, &firstName, &lastName, &senderID, &unixtime, &text); err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}

		sender := resolveSenderName(username, firstName, lastName, senderID)
		score := scoreSearchResult(text, query, variants, patterns)
		if score <= 0 {
			continue
		}

		ranked = append(ranked, rankedSearchMessageResult{
			searchMessageResult: searchMessageResult{
				Sender:    sender,
				Text:      text,
				Timestamp: unixtime,
			},
			score: score,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating search results: %w", err)
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].Timestamp > ranked[j].Timestamp
		}
		return ranked[i].score > ranked[j].score
	})

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	results := make([]searchMessageResult, 0, len(ranked))
	for _, item := range ranked {
		results = append(results, item.searchMessageResult)
	}

	return results, nil
}

func buildSearchPatterns(query string, variants []string) []string {
	terms := append([]string{query}, variants...)

	seen := make(map[string]struct{})
	patterns := make([]string, 0, len(terms)*4)
	appendPattern := func(value string) {
		value = normalizeSearchText(value)
		if value == "" {
			return
		}

		value = strings.TrimSpace(value)
		if len([]rune(value)) < 3 {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		patterns = append(patterns, value)
	}

	for _, term := range terms {
		term = normalizeSearchText(term)
		if term == "" {
			continue
		}

		appendPattern(term)
		for _, token := range querySplitPattern.FindAllString(term, -1) {
			if _, skip := searchStopWords[token]; skip {
				continue
			}
			appendPattern(token)
		}
	}

	return patterns
}

func scoreSearchResult(text, rawQuery string, variants []string, patterns []string) int {
	normalizedText := normalizeSearchText(text)
	if normalizedText == "" {
		return 0
	}

	queries := make([]string, 0, len(variants)+1)
	queries = append(queries, rawQuery)
	queries = append(queries, variants...)

	textTokens := map[string]struct{}{}
	for _, token := range querySplitPattern.FindAllString(normalizedText, -1) {
		textTokens[token] = struct{}{}
	}

	score := 0
	for _, raw := range queries {
		normalized := normalizeSearchText(raw)
		if normalized == "" {
			continue
		}

		switch {
		case normalizedText == normalized:
			score += 200
		case strings.Contains(normalizedText, normalized):
			score += 120
		}
	}

	for _, pattern := range patterns {
		if _, ok := textTokens[pattern]; ok {
			score += 80
			continue
		}

		matched := false
		for token := range textTokens {
			switch {
			case strings.HasPrefix(token, pattern):
				score += 55
				matched = true
			case strings.Contains(token, pattern):
				score += max(15, len([]rune(pattern))*searchSubstringPenalty)
				matched = true
			}
			if matched {
				break
			}
		}

		if !matched && strings.Contains(normalizedText, pattern) {
			score += 10
		}
	}

	return score
}

func normalizeSearchText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(value))
	lastSpace := true
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		case !lastSpace:
			b.WriteByte(' ')
			lastSpace = true
		}
	}

	return strings.TrimSpace(b.String())
}

func resolveSenderName(username, firstName, lastName sql.NullString, senderID int64) string {
	name := strings.TrimSpace(strings.Join([]string{firstName.String, lastName.String}, " "))
	sender := fmt.Sprintf("user_%d", senderID)
	switch {
	case username.Valid && username.String != "":
		sender = username.String
	case name != "":
		sender = name
	}
	return sender
}
