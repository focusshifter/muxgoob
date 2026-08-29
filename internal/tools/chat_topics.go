package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"

	chatmemory "github.com/focusshifter/muxgoob/internal/memory"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/facts"
)

type RememberTopicTool struct {
	db     *sql.DB
	chatID int64
}

type ForgetTopicTool struct {
	db      *sql.DB
	chatID  int64
	matcher topicMatcher
}

type topicMatcher func(ctx context.Context, chatID int64, bullets []string, topic string) ([]string, error)

var defaultTopicMatcher topicMatcher = aiTopicMatcher

type rememberTopicArgs struct {
	Topic string `json:"topic"`
}

type forgetTopicArgs struct {
	Topic string `json:"topic"`
}

type chatTopicResult struct {
	Action        string   `json:"action"`
	Topic         string   `json:"topic"`
	Changed       bool     `json:"changed"`
	Added         string   `json:"added,omitempty"`
	Removed       []string `json:"removed,omitempty"`
	StableContext []string `json:"stable_context"`
	Version       int      `json:"version,omitempty"`
}

func NewRememberTopicTool(db *sql.DB, chatID int64) *RememberTopicTool {
	return &RememberTopicTool{db: db, chatID: chatID}
}

func NewForgetTopicTool(db *sql.DB, chatID int64) *ForgetTopicTool {
	return &ForgetTopicTool{db: db, chatID: chatID, matcher: defaultTopicMatcher}
}

func (t *RememberTopicTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "rememberTopic",
			Description: "Add a durable chat topic, recurring lore point, or persistent preference to the chat's stable context when the user explicitly asks the bot to remember it.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "The durable topic or instruction to remember as one stable-context bullet.",
					},
				},
				"required": []string{"topic"},
			},
		},
	}
}

func (t *ForgetTopicTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "forgetTopic",
			Description: "Remove remembered chat-topic lines from the chat prompt when the user explicitly asks the bot to forget them.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "The topic to forget. The tool will match it against the current chat-prompt bullets and remove the relevant line or lines.",
					},
				},
				"required": []string{"topic"},
			},
		},
	}
}

func (t *RememberTopicTool) Execute(ctx context.Context, args string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	if err := chatmemory.EnsureSchema(t.db); err != nil {
		return "", err
	}

	parsedArgs := rememberTopicArgs{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	topic := strings.TrimSpace(parsedArgs.Topic)
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	cleanTopic := sanitizeStableContextTopic(topic)
	if cleanTopic == "" {
		return "", fmt.Errorf("topic is required")
	}

	entry, changed, err := chatmemory.NewRepository(t.db).Add(ctx, chatmemory.Entry{
		ChatID: t.chatID, Kind: chatmemory.ChatLore, Body: cleanTopic, SourceType: "rememberTopic",
	})
	if err != nil {
		return "", err
	}
	stableContext := []string{entry.Body}
	if !chatmemory.IsCutover(ctx, t.db, t.chatID) {
		currentPrompt, _, promptErr := getLatestChatPrompt(t.db, t.chatID)
		if promptErr != nil {
			return "", promptErr
		}
		stableContext = append(stableContext, facts.ParseChatPrompt(currentPrompt).StableContext...)
	}
	return marshalJSON(chatTopicResult{Action: "remember", Topic: topic, Changed: changed, Added: entry.Body, StableContext: stableContext}), nil
}

func (t *ForgetTopicTool) Execute(ctx context.Context, args string) (string, error) {
	if t == nil || t.db == nil {
		return "", fmt.Errorf("database is not initialized")
	}
	if err := chatmemory.EnsureSchema(t.db); err != nil {
		return "", err
	}

	parsedArgs := forgetTopicArgs{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	topic := strings.TrimSpace(parsedArgs.Topic)
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	cutover := chatmemory.IsCutover(ctx, t.db, t.chatID)
	currentPrompt := ""
	currentVersion := 0
	if !cutover {
		var err error
		currentPrompt, currentVersion, err = getLatestChatPrompt(t.db, t.chatID)
		if err != nil {
			return "", err
		}
	}

	parsed := facts.ParseChatPrompt(currentPrompt)
	entries, err := chatmemory.NewRepository(t.db).List(ctx, chatmemory.Filter{ChatID: t.chatID, Kind: chatmemory.ChatLore})
	if err != nil {
		return "", err
	}
	allBullets := append([]string(nil), parsed.StableContext...)
	for _, entry := range entries {
		allBullets = append(allBullets, entry.Body)
	}
	if len(allBullets) == 0 {
		return marshalJSON(chatTopicResult{Action: "forget", Topic: topic, Changed: false, StableContext: parsed.StableContext}), nil
	}

	matcher := t.matcher
	if matcher == nil {
		matcher = defaultTopicMatcher
	}
	toRemove, err := matcher(ctx, t.chatID, allBullets, topic)
	if err != nil {
		return "", err
	}

	removeSet := make(map[string]struct{}, len(toRemove))
	for _, item := range toRemove {
		normalized := normalizeSearchText(item)
		if normalized != "" {
			removeSet[normalized] = struct{}{}
		}
	}

	if len(removeSet) == 0 {
		return marshalJSON(chatTopicResult{Action: "forget", Topic: topic, Changed: false, StableContext: parsed.StableContext}), nil
	}

	var removed []string
	parsed.StableContext, removed = filterPromptSection(parsed.StableContext, removeSet)
	for _, entry := range entries {
		if _, ok := removeSet[normalizeSearchText(entry.Body)]; !ok {
			continue
		}
		removed = append(removed, entry.Body)
	}

	if len(removed) == 0 {
		return marshalJSON(chatTopicResult{Action: "forget", Topic: topic, Changed: false, StableContext: parsed.StableContext}), nil
	}

	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for _, entry := range entries {
		if _, ok := removeSet[normalizeSearchText(entry.Body)]; !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memory_entries SET status=?,updated_at=? WHERE id=? AND status=?`, chatmemory.Archived, time.Now().Unix(), entry.ID, chatmemory.Active); err != nil {
			return "", err
		}
	}
	version := 0
	if !cutover && currentPrompt != "" {
		updatedPrompt := facts.RenderChatPrompt(parsed)
		version = currentVersion + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO prompts(chat_id,version,prompt,created_at) VALUES(?,?,?,?)`, t.chatID, version, updatedPrompt, time.Now().Unix()); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}

	stableContext := append([]string(nil), parsed.StableContext...)
	for _, entry := range entries {
		if _, removedEntry := removeSet[normalizeSearchText(entry.Body)]; !removedEntry {
			stableContext = append(stableContext, entry.Body)
		}
	}
	return marshalJSON(chatTopicResult{
		Action:        "forget",
		Topic:         topic,
		Changed:       true,
		Removed:       removed,
		StableContext: stableContext,
		Version:       version,
	}), nil
}

func promptBullets(prompt *facts.ChatPrompt) []string {
	if prompt == nil {
		return nil
	}
	b := make([]string, 0, len(prompt.ReplyStyle)+len(prompt.StableContext)+len(prompt.Avoid))
	b = append(b, prompt.ReplyStyle...)
	b = append(b, prompt.StableContext...)
	b = append(b, prompt.Avoid...)
	return b
}

func filterPromptSection(items []string, removeSet map[string]struct{}, removed ...string) ([]string, []string) {
	remaining := make([]string, 0, len(items))
	resultRemoved := append([]string(nil), removed...)
	for _, item := range items {
		if _, ok := removeSet[normalizeSearchText(item)]; ok {
			resultRemoved = append(resultRemoved, item)
			continue
		}
		remaining = append(remaining, item)
	}
	return remaining, resultRemoved
}

func sanitizeStableContextTopic(topic string) string {
	topic = strings.TrimSpace(topic)
	topic = strings.TrimPrefix(topic, "- ")
	topic = strings.TrimSpace(topic)
	return topic
}

func getLatestChatPrompt(db *sql.DB, chatID int64) (string, int, error) {
	var prompt string
	var version int
	err := db.QueryRow(`
		SELECT prompt, version FROM prompts
		WHERE chat_id = ?
		ORDER BY version DESC LIMIT 1`, chatID).Scan(&prompt, &version)
	if err == sql.ErrNoRows {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("retrieving chat prompt: %w", err)
	}
	return prompt, version, nil
}

func saveChatPrompt(db *sql.DB, chatID int64, prompt string) (int, error) {
	var nextVersion int
	err := db.QueryRow(`SELECT COALESCE(MAX(version) + 1, 1) FROM prompts WHERE chat_id = ?`, chatID).Scan(&nextVersion)
	if err != nil {
		return 0, fmt.Errorf("getting next prompt version: %w", err)
	}

	_, err = db.Exec(`INSERT INTO prompts (chat_id, version, prompt, created_at) VALUES (?, ?, ?, ?)`, chatID, nextVersion, prompt, time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("saving chat prompt: %w", err)
	}

	return nextVersion, nil
}

func aiTopicMatcher(ctx context.Context, chatID int64, bullets []string, topic string) ([]string, error) {
	client, model := buildLightweightClient(chatID)
	if client == nil {
		return nil, fmt.Errorf("lightweight client is not configured")
	}

	b, err := json.Marshal(bullets)
	if err != nil {
		return nil, fmt.Errorf("encoding stable context: %w", err)
	}

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You match a forget request to exact chat-prompt bullets. Return JSON only: {\"remove\":[\"exact bullet\"]}. Remove only bullets that clearly match the topic. If nothing matches, return {\"remove\":[]}."},
			{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf("Topic to forget: %s\nChat prompt bullets JSON: %s", topic, string(b))},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("matching topic to prompt bullets: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("matcher returned no choices")
	}

	var payload struct {
		Remove []string `json:"remove"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Choices[0].Message.Content)), &payload); err != nil {
		return nil, fmt.Errorf("parsing matcher output: %w", err)
	}

	return payload.Remove, nil
}

func buildLightweightClient(chatID int64) (ChatCompletionCreator, string) {
	chatIDPtr := &chatID
	aiProvider := registry.GetAiProvider(chatIDPtr)
	switch aiProvider {
	case "openrouter":
		config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		client := openai.NewClientWithConfig(config)
		return client, "openai/gpt-4o-mini"
	case "openai-codex":
		model := strings.TrimSpace(registry.GetAiModel(chatIDPtr))
		if model == "" {
			model = "gpt-5.4"
		}
		modelInfo := openaicodex.NormalizeConfiguredModel(model)
		if modelInfo.UseCodex {
			fallbackConfig := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
			fallbackConfig.BaseURL = "https://openrouter.ai/api/v1"
			return openaicodex.NewClient(openaicodex.WithFallbackClient(openai.NewClientWithConfig(fallbackConfig))), modelInfo.RawModel
		}
		config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
		config.BaseURL = "https://openrouter.ai/api/v1"
		return openai.NewClientWithConfig(config), modelInfo.OpenRouterModel
	default:
		if strings.TrimSpace(registry.Config.OpenaiApiKey) == "" {
			return nil, ""
		}
		config := openai.DefaultConfig(registry.Config.OpenaiApiKey)
		client := openai.NewClientWithConfig(config)
		return client, "gpt-4o-mini"
	}
}
