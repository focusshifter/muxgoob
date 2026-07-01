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

	"github.com/focusshifter/muxgoob/database"
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

	results, err := searchMessagesWithFTS(db, chatID, excludeMessageID, query, variants, patterns, limit)
	if err != nil {
		fallbackResults, fallbackErr := searchMessagesWithLike(db, chatID, excludeMessageID, query, variants, patterns, limit)
		if fallbackErr != nil {
			return nil, err
		}
		results = fallbackResults
	}

	metadataResults, metadataErr := searchImageMetadataWithLike(db, chatID, excludeMessageID, query, variants, patterns, limit)
	if metadataErr == nil && len(metadataResults) > 0 {
		results = mergeSearchResults(results, metadataResults, limit)
	}
	return results, nil
}

func searchMessagesWithFTS(db *sql.DB, chatID int64, excludeMessageID int, query string, variants, patterns []string, limit int) ([]searchMessageResult, error) {
	if err := database.EnsureMessageSearchIndex(db); err != nil {
		return nil, err
	}

	matchQuery := buildFTSQuery(query, variants)
	if strings.TrimSpace(matchQuery) == "" {
		return nil, nil
	}

	ranked := make([]rankedSearchMessageResult, 0)
	strictMatchQuery := buildStrictFTSQuery(query, variants)
	if strictMatchQuery != "" && strictMatchQuery != matchQuery {
		strictRanked, err := queryRankedMessagesWithFTS(db, chatID, excludeMessageID, strictMatchQuery, query, variants, patterns, limit)
		if err != nil {
			return nil, err
		}
		ranked = append(ranked, strictRanked...)
	}

	broadRanked, err := queryRankedMessagesWithFTS(db, chatID, excludeMessageID, matchQuery, query, variants, patterns, limit)
	if err != nil {
		return nil, err
	}
	ranked = append(ranked, broadRanked...)

	return finalizeRankedSearchResults(dedupeRankedSearchResults(ranked), limit), nil
}

func queryRankedMessagesWithFTS(db *sql.DB, chatID int64, excludeMessageID int, matchQuery, query string, variants, patterns []string, limit int) ([]rankedSearchMessageResult, error) {
	args := make([]any, 0, 4)
	args = append(args, matchQuery, chatID)
	queryFilter := "WHERE messages_fts MATCH ? AND m.chat_id = ?"
	if excludeMessageID > 0 {
		queryFilter += " AND m.id != ?"
		args = append(args, excludeMessageID)
	}
	candidateLimit := max(searchCandidateLimit, limit*5)
	args = append(args, candidateLimit)

	rows, err := db.Query(`
		SELECT u.username, u.first_name, u.last_name, m.sender_id, m.unixtime, COALESCE(NULLIF(m.text, ''), m.caption, ''), bm25(messages_fts)
		FROM messages_fts
		JOIN messages m ON m.rowid = messages_fts.rowid
		LEFT JOIN users u ON u.id = m.sender_id
		`+queryFilter+`
		ORDER BY bm25(messages_fts), m.unixtime DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages with fts: %w", err)
	}
	defer rows.Close()

	ranked, err := collectRankedSearchResults(rows, query, variants, patterns)
	if err != nil {
		return nil, err
	}
	return ranked, nil
}

func searchMessagesWithLike(db *sql.DB, chatID int64, excludeMessageID int, query string, variants, patterns []string, limit int) ([]searchMessageResult, error) {
	ranked := make([]rankedSearchMessageResult, 0)

	strictWhereParts, strictArgs := buildStrictLikeFilters(query, variants, "LOWER(COALESCE(NULLIF(m.text, ''), m.caption, ''))")
	if len(strictWhereParts) > 0 {
		strictRanked, err := queryRankedMessagesWithLike(db, chatID, excludeMessageID, query, variants, patterns, strictWhereParts, strictArgs, limit)
		if err != nil {
			return nil, err
		}
		ranked = append(ranked, strictRanked...)
	}

	whereParts := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns))
	for _, pattern := range patterns {
		whereParts = append(whereParts, "LOWER(COALESCE(NULLIF(m.text, ''), m.caption, '')) LIKE ?")
		args = append(args, "%"+pattern+"%")
	}

	broadRanked, err := queryRankedMessagesWithLike(db, chatID, excludeMessageID, query, variants, patterns, whereParts, args, limit)
	if err != nil {
		return nil, err
	}
	ranked = append(ranked, broadRanked...)

	return finalizeRankedSearchResults(dedupeRankedSearchResults(ranked), limit), nil
}

func queryRankedMessagesWithLike(db *sql.DB, chatID int64, excludeMessageID int, query string, variants, patterns []string, whereParts []string, matchArgs []any, limit int) ([]rankedSearchMessageResult, error) {
	args := make([]any, 0, len(matchArgs)+3)
	args = append(args, chatID)
	if excludeMessageID > 0 {
		args = append(args, excludeMessageID)
	}
	args = append(args, matchArgs...)
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

	ranked, err := collectRankedSearchResults(rows, query, variants, patterns)
	if err != nil {
		return nil, err
	}
	return ranked, nil
}

func searchImageMetadataWithLike(db *sql.DB, chatID int64, excludeMessageID int, query string, variants, patterns []string, limit int) ([]searchMessageResult, error) {
	whereParts := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns)+3)
	args = append(args, chatID)
	if excludeMessageID > 0 {
		args = append(args, excludeMessageID)
	}
	for _, pattern := range patterns {
		whereParts = append(whereParts, "LOWER(COALESCE(mm.description, '') || ' ' || COALESCE(mm.visible_text, '') || ' ' || COALESCE(mm.tags, '')) LIKE ?")
		args = append(args, "%"+pattern+"%")
	}
	args = append(args, searchCandidateLimit)

	queryFilter := "WHERE mm.chat_id = ? AND mm.status = 'done'"
	if excludeMessageID > 0 {
		queryFilter += " AND mm.message_id != ?"
	}

	rows, err := db.Query(`
		SELECT u.username, u.first_name, u.last_name, m.sender_id, m.unixtime,
		       '[image] ' || TRIM(COALESCE(mm.description, '') || CASE WHEN COALESCE(mm.visible_text, '') != '' THEN ' Visible text: ' || mm.visible_text ELSE '' END)
		FROM media_metadata mm
		JOIN messages m ON m.chat_id = mm.chat_id AND m.id = mm.message_id
		LEFT JOIN users u ON u.id = m.sender_id
		`+queryFilter+`
		  AND (`+strings.Join(whereParts, " OR ")+`)
		ORDER BY m.unixtime DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("searching image metadata: %w", err)
	}
	defer rows.Close()

	ranked, err := collectRankedSearchResults(rows, query, variants, patterns)
	if err != nil {
		return nil, err
	}
	return finalizeRankedSearchResults(ranked, limit), nil
}

func mergeSearchResults(primary, extra []searchMessageResult, limit int) []searchMessageResult {
	merged := make([]searchMessageResult, 0, len(primary)+len(extra))
	seen := make(map[string]struct{})
	appendUnique := func(items []searchMessageResult) {
		for _, item := range items {
			key := fmt.Sprintf("%d:%s:%s", item.Timestamp, item.Sender, item.Text)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, item)
		}
	}
	appendUnique(primary)
	appendUnique(extra)
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Timestamp > merged[j].Timestamp })
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func collectRankedSearchResults(rows *sql.Rows, query string, variants, patterns []string) ([]rankedSearchMessageResult, error) {
	ranked := make([]rankedSearchMessageResult, 0)
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading search result columns: %w", err)
	}
	includeBM25 := len(columns) >= 7

	for rows.Next() {
		var username, firstName, lastName sql.NullString
		var senderID int64
		var unixtime int64
		var text string
		var bm25Score float64
		if includeBM25 {
			if err := rows.Scan(&username, &firstName, &lastName, &senderID, &unixtime, &text, &bm25Score); err != nil {
				return nil, fmt.Errorf("scanning search result: %w", err)
			}
		} else {
			if err := rows.Scan(&username, &firstName, &lastName, &senderID, &unixtime, &text); err != nil {
				return nil, fmt.Errorf("scanning search result: %w", err)
			}
		}

		sender := resolveSenderName(username, firstName, lastName, senderID)
		score := scoreSearchResult(text, query, variants, patterns)
		if includeBM25 {
			score += convertBM25ToBoost(bm25Score)
		}
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
	return ranked, nil
}

func finalizeRankedSearchResults(ranked []rankedSearchMessageResult, limit int) []searchMessageResult {
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
	return results
}

func buildStrictLikeFilters(query string, variants []string, expression string) ([]string, []any) {
	terms := append([]string{query}, variants...)
	seen := make(map[string]struct{})
	whereParts := make([]string, 0, len(terms))
	args := make([]any, 0)

	for _, term := range terms {
		normalized := normalizeSearchText(term)
		if normalized == "" {
			continue
		}

		tokens := make([]string, 0, 4)
		for _, token := range querySplitPattern.FindAllString(normalized, -1) {
			if _, skip := searchStopWords[token]; skip {
				continue
			}
			if len([]rune(token)) < 2 {
				continue
			}
			tokens = append(tokens, token)
		}
		if len(tokens) < 2 {
			continue
		}

		key := strings.Join(tokens, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		andParts := make([]string, 0, len(tokens))
		for _, token := range tokens {
			andParts = append(andParts, expression+" LIKE ?")
			args = append(args, "%"+token+"%")
		}
		whereParts = append(whereParts, "("+strings.Join(andParts, " AND ")+")")
	}

	return whereParts, args
}

func dedupeRankedSearchResults(ranked []rankedSearchMessageResult) []rankedSearchMessageResult {
	if len(ranked) == 0 {
		return ranked
	}

	seen := make(map[string]int, len(ranked))
	deduped := make([]rankedSearchMessageResult, 0, len(ranked))
	for _, item := range ranked {
		key := fmt.Sprintf("%d:%s:%s", item.Timestamp, item.Sender, item.Text)
		if idx, ok := seen[key]; ok {
			if item.score > deduped[idx].score {
				deduped[idx] = item
			}
			continue
		}
		seen[key] = len(deduped)
		deduped = append(deduped, item)
	}
	return deduped
}

func buildStrictFTSQuery(query string, variants []string) string {
	terms := append([]string{query}, variants...)
	seen := make(map[string]struct{})
	clauses := make([]string, 0, len(terms))

	for _, term := range terms {
		normalized := normalizeSearchText(term)
		if normalized == "" {
			continue
		}

		tokens := make([]string, 0, 4)
		for _, token := range querySplitPattern.FindAllString(normalized, -1) {
			if _, skip := searchStopWords[token]; skip {
				continue
			}
			if len([]rune(token)) < 2 {
				continue
			}
			tokens = append(tokens, token)
		}
		if len(tokens) == 0 {
			continue
		}

		clause := strings.Join(tokens, " AND ")
		if len(tokens) > 1 {
			clause = "(" + clause + ")"
		}
		if _, ok := seen[clause]; ok {
			continue
		}
		seen[clause] = struct{}{}
		clauses = append(clauses, clause)
	}

	return strings.Join(clauses, " OR ")
}

func buildFTSQuery(query string, variants []string) string {
	terms := append([]string{query}, variants...)
	seen := make(map[string]struct{})
	clauses := make([]string, 0, len(terms)*3)
	appendClause := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		clauses = append(clauses, value)
	}

	for _, term := range terms {
		normalized := normalizeSearchText(term)
		if normalized == "" {
			continue
		}

		tokens := make([]string, 0, 4)
		for _, token := range querySplitPattern.FindAllString(normalized, -1) {
			if _, skip := searchStopWords[token]; skip {
				continue
			}
			if len([]rune(token)) < 2 {
				continue
			}
			tokens = append(tokens, token)
		}
		if len(tokens) == 0 {
			continue
		}

		if len(tokens) > 1 {
			appendClause(`"` + strings.Join(tokens, " ") + `"`)
			appendClause(strings.Join(tokens, " AND "))
			continue
		}

		appendClause(tokens[0])
		if len([]rune(tokens[0])) >= 4 {
			appendClause(tokens[0] + "*")
		}
	}

	return strings.Join(clauses, " OR ")
}

func convertBM25ToBoost(score float64) int {
	switch {
	case score <= -20:
		return 220
	case score <= -10:
		return 180
	case score <= -5:
		return 140
	case score <= -2:
		return 100
	case score < 0:
		return 60
	default:
		return 25
	}
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

		queryTokens := make([]string, 0, 4)
		for _, token := range querySplitPattern.FindAllString(normalized, -1) {
			if _, skip := searchStopWords[token]; skip {
				continue
			}
			if len([]rune(token)) < 2 {
				continue
			}
			queryTokens = append(queryTokens, token)
		}
		if len(queryTokens) > 1 {
			matchedTokens := 0
			for _, queryToken := range queryTokens {
				if searchTextContainsToken(textTokens, queryToken) {
					matchedTokens++
				}
			}
			score += matchedTokens * 40
			if matchedTokens == len(queryTokens) {
				score += 180
			}
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

func searchTextContainsToken(textTokens map[string]struct{}, queryToken string) bool {
	for textToken := range textTokens {
		if textToken == queryToken || strings.HasPrefix(textToken, queryToken) || strings.Contains(textToken, queryToken) {
			return true
		}
	}
	return false
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
