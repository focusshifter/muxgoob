package openaicodex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const (
	defaultBaseURL      = "https://chatgpt.com/backend-api/codex"
	defaultAuthBaseURL  = "https://auth.openai.com"
	codexClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultInstructions = "You are a helpful assistant."
)

type Option func(*Client)

type ChatCompletionCreator interface {
	CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

type Client struct {
	baseURL        string
	authBaseURL    string
	httpClient     *http.Client
	codexHome      string
	fallbackClient ChatCompletionCreator
}

type fallbackDisabledContextKey struct{}

// WithoutFallback marks a completion context as ineligible for provider fallback.
// Image-generation requests use it so a Codex failure cannot be converted into a
// text-only OpenRouter completion.
func WithoutFallback(ctx context.Context) context.Context {
	return context.WithValue(ctx, fallbackDisabledContextKey{}, true)
}

func fallbackEnabled(ctx context.Context, client *Client) bool {
	disabled, _ := ctx.Value(fallbackDisabledContextKey{}).(bool)
	return client.fallbackClient != nil && !disabled
}

func NewClient(opts ...Option) *Client {
	client := &Client{
		baseURL:     defaultBaseURL,
		authBaseURL: defaultAuthBaseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client
}

func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

func WithAuthBaseURL(authBaseURL string) Option {
	return func(c *Client) {
		c.authBaseURL = strings.TrimRight(strings.TrimSpace(authBaseURL), "/")
	}
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func WithCodexHome(codexHome string) Option {
	return func(c *Client) {
		c.codexHome = strings.TrimSpace(codexHome)
	}
}

func WithFallbackClient(fallback ChatCompletionCreator) Option {
	return func(c *Client) {
		c.fallbackClient = fallback
	}
}

type codexAuthFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
}

type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type codexRequest struct {
	Model            string   `json:"model"`
	Instructions     string   `json:"instructions"`
	Store            bool     `json:"store"`
	Stream           bool     `json:"stream"`
	Input            []any    `json:"input"`
	Tools            []any    `json:"tools,omitempty"`
	Temperature      *float32 `json:"temperature,omitempty"`
	TopP             *float32 `json:"top_p,omitempty"`
	FrequencyPenalty *float32 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float32 `json:"presence_penalty,omitempty"`
	MaxOutputTokens  int      `json:"max_output_tokens,omitempty"`
}

type codexTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexMessageInput struct {
	Role    string          `json:"role"`
	Content []codexTextPart `json:"content"`
}

type codexFunctionCallInput struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type codexFunctionCallOutputInput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type codexTool struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
	Strict      bool   `json:"strict"`
}

type codexNativeWebSearchTool struct {
	Type              string `json:"type"`
	ExternalWebAccess bool   `json:"external_web_access"`
}

type NormalizedConfiguredModel struct {
	RawModel        string
	Model           string
	UseCodex        bool
	NativeWebSearch bool
	OpenRouterModel string
}

func NormalizeConfiguredModel(model string) NormalizedConfiguredModel {
	raw := strings.TrimSpace(model)
	normalized := NormalizedConfiguredModel{
		RawModel: raw,
		Model:    raw,
		UseCodex: true,
	}
	if raw == "" {
		return normalized
	}

	rawForRouter := raw
	if strings.HasPrefix(rawForRouter, "openrouter/") {
		rawForRouter = strings.TrimPrefix(rawForRouter, "openrouter/")
	}
	normalized.OpenRouterModel = rawForRouter

	trimmed := rawForRouter
	if strings.HasSuffix(trimmed, ":online") {
		normalized.NativeWebSearch = true
		trimmed = strings.TrimSuffix(trimmed, ":online")
	}
	if strings.HasPrefix(trimmed, "openai/") {
		trimmed = strings.TrimPrefix(trimmed, "openai/")
	} else if strings.Contains(trimmed, "/") {
		normalized.UseCodex = false
		normalized.Model = trimmed
		normalized.NativeWebSearch = false
		return normalized
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		trimmed = raw
	}
	if !strings.HasPrefix(trimmed, "gpt-") {
		normalized.UseCodex = false
		normalized.NativeWebSearch = false
	} else if !strings.Contains(normalized.OpenRouterModel, "/") {
		normalized.OpenRouterModel = "openai/" + normalized.OpenRouterModel
	}
	normalized.Model = trimmed
	return normalized
}

type codexEvent struct {
	Type     string `json:"type"`
	Response struct {
		ID        string `json:"id"`
		CreatedAt int64  `json:"created_at"`
		Model     string `json:"model"`
		Usage     *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
		Error any `json:"error"`
	} `json:"response"`
	Item struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		Status    string `json:"status"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	payload, err := c.buildRequest(request)
	if err != nil {
		return openai.ChatCompletionResponse{}, err
	}

	authPath, accessToken, err := c.loadAuthFile()
	if err != nil {
		if fallbackEnabled(ctx, c) {
			fallback := fallbackRequest(request)
			log.Printf("[openaicodex] auth unavailable, falling back to openrouter model=%s", fallback.Model)
			resp, fallbackErr := c.fallbackClient.CreateChatCompletion(ctx, fallback)
			log.Printf("[openaicodex] fallback completed model=%s choices=%d err=%v", fallback.Model, len(resp.Choices), fallbackErr)
			return resp, fallbackErr
		}
		return openai.ChatCompletionResponse{}, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("marshal codex request: %w", err)
	}
	log.Printf("[openaicodex] request configured_model=%s payload_model=%s input_items=%d tools=%d fallback=%t", request.Model, payload.Model, len(payload.Input), len(payload.Tools), c.fallbackClient != nil)

	url := strings.TrimRight(c.baseURL, "/") + "/responses"
	for attempt := 1; attempt <= 2; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return openai.ChatCompletionResponse{}, fmt.Errorf("build codex request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+accessToken)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")

		httpResp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if fallbackEnabled(ctx, c) {
				fallback := fallbackRequest(request)
				log.Printf("[openaicodex] transport error, falling back to openrouter model=%s err=%v", fallback.Model, err)
				resp, fallbackErr := c.fallbackClient.CreateChatCompletion(ctx, fallback)
				log.Printf("[openaicodex] fallback completed model=%s choices=%d err=%v", fallback.Model, len(resp.Choices), fallbackErr)
				return resp, fallbackErr
			}
			return openai.ChatCompletionResponse{}, fmt.Errorf("send codex request: %w", err)
		}

		if httpResp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(httpResp.Body)
			httpResp.Body.Close()
			if httpResp.StatusCode == http.StatusUnauthorized && attempt == 1 {
				refreshedToken, refreshErr := c.refreshAccessToken(ctx, authPath)
				if refreshErr == nil {
					log.Printf("[openaicodex] refreshed expired codex token")
					accessToken = refreshedToken
					continue
				}
				log.Printf("[openaicodex] token refresh failed: %v", refreshErr)
			}
			if fallbackEnabled(ctx, c) {
				fallback := fallbackRequest(request)
				log.Printf("[openaicodex] backend error status=%d, falling back to openrouter model=%s body=%s", httpResp.StatusCode, fallback.Model, strings.TrimSpace(string(respBody)))
				resp, fallbackErr := c.fallbackClient.CreateChatCompletion(ctx, fallback)
				log.Printf("[openaicodex] fallback completed model=%s choices=%d err=%v", fallback.Model, len(resp.Choices), fallbackErr)
				return resp, fallbackErr
			}
			return openai.ChatCompletionResponse{}, fmt.Errorf("codex responses error: status=%d body=%s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		resp, parseErr := parseSSE(httpResp.Body)
		httpResp.Body.Close()
		if parseErr != nil {
			return openai.ChatCompletionResponse{}, parseErr
		}
		if shouldRetryEmptyStop(resp) {
			if attempt < 2 {
				log.Printf("[openaicodex] empty stop response on attempt=%d, retrying once", attempt)
				continue
			}
			if fallbackEnabled(ctx, c) {
				fallback := fallbackRequest(request)
				log.Printf("[openaicodex] empty stop after retry, falling back to openrouter model=%s", fallback.Model)
				fallbackResp, fallbackErr := c.fallbackClient.CreateChatCompletion(ctx, fallback)
				log.Printf("[openaicodex] fallback completed model=%s choices=%d err=%v", fallback.Model, len(fallbackResp.Choices), fallbackErr)
				return fallbackResp, fallbackErr
			}
		}
		return resp, nil
	}

	return openai.ChatCompletionResponse{}, fmt.Errorf("codex request exhausted retries")
}

func fallbackRequest(request openai.ChatCompletionRequest) openai.ChatCompletionRequest {
	clone := request
	modelInfo := NormalizeConfiguredModel(request.Model)
	if strings.TrimSpace(modelInfo.OpenRouterModel) != "" {
		clone.Model = modelInfo.OpenRouterModel
	}
	return clone
}

func shouldRetryEmptyStop(resp openai.ChatCompletionResponse) bool {
	if len(resp.Choices) == 0 {
		return false
	}
	choice := resp.Choices[0]
	if choice.FinishReason != openai.FinishReasonStop {
		return false
	}
	if strings.TrimSpace(choice.Message.Content) != "" {
		return false
	}
	return len(choice.Message.ToolCalls) == 0
}

func (c *Client) buildRequest(request openai.ChatCompletionRequest) (codexRequest, error) {
	modelInfo := NormalizeConfiguredModel(request.Model)
	instructions := defaultInstructions
	inputs := make([]any, 0, len(request.Messages))
	systemParts := make([]string, 0, 2)
	for _, message := range request.Messages {
		content := strings.TrimSpace(extractMessageText(message))
		switch message.Role {
		case openai.ChatMessageRoleSystem:
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case openai.ChatMessageRoleUser, openai.ChatMessageRoleAssistant:
			if content != "" {
				inputs = append(inputs, codexMessageInput{
					Role: message.Role,
					Content: []codexTextPart{{
						Type: "input_text",
						Text: content,
					}},
				})
			}
			for _, toolCall := range message.ToolCalls {
				inputs = append(inputs, codexFunctionCallInput{
					Type:      "function_call",
					CallID:    toolCall.ID,
					Name:      toolCall.Function.Name,
					Arguments: toolCall.Function.Arguments,
				})
			}
		case openai.ChatMessageRoleTool:
			inputs = append(inputs, codexFunctionCallOutputInput{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: message.Content,
			})
		default:
			if content != "" {
				inputs = append(inputs, codexMessageInput{
					Role:    message.Role,
					Content: []codexTextPart{{Type: "input_text", Text: content}},
				})
			}
		}
	}
	if len(systemParts) > 0 {
		instructions = strings.Join(systemParts, "\n\n")
	}

	functionTools := make([]codexTool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if tool.Type != openai.ToolTypeFunction || tool.Function == nil {
			continue
		}
		functionTools = append(functionTools, codexTool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  normalizeSchemaForCodex(tool.Function.Parameters),
			Strict:      true,
		})
	}

	var err error
	functionTools, instructions, err = applyToolChoice(functionTools, request.ToolChoice, instructions)
	if err != nil {
		return codexRequest{}, err
	}

	tools := make([]any, 0, len(functionTools)+1)
	for _, tool := range functionTools {
		tools = append(tools, tool)
	}
	if modelInfo.NativeWebSearch {
		tools = append(tools, codexNativeWebSearchTool{
			Type:              "web_search",
			ExternalWebAccess: true,
		})
	}

	payload := codexRequest{
		Model:        modelInfo.Model,
		Instructions: instructions,
		Store:        false,
		Stream:       true,
		Input:        inputs,
		Tools:        tools,
	}
	if request.MaxCompletionTokens > 0 {
		payload.MaxOutputTokens = request.MaxCompletionTokens
	} else if request.MaxTokens > 0 {
		payload.MaxOutputTokens = request.MaxTokens
	}
	return payload, nil
}

func applyToolChoice(tools []codexTool, toolChoice any, instructions string) ([]codexTool, string, error) {
	if toolChoice == nil {
		return tools, instructions, nil
	}
	if mode, ok := toolChoice.(string); ok {
		switch mode {
		case "", "auto":
			return tools, instructions, nil
		case "none":
			return nil, instructions, nil
		case "required":
			if len(tools) == 0 {
				return nil, "", fmt.Errorf("tool_choice=required but no tools were provided")
			}
			return tools, appendInstruction(instructions, "You must call one of the available tools before responding."), nil
		default:
			return tools, instructions, nil
		}
	}
	forcedChoice, ok := toolChoice.(openai.ToolChoice)
	if !ok || forcedChoice.Type != openai.ToolTypeFunction {
		return tools, instructions, nil
	}
	targetName := strings.TrimSpace(forcedChoice.Function.Name)
	if targetName == "" {
		return nil, "", fmt.Errorf("tool_choice.function.name is required")
	}
	filtered := make([]codexTool, 0, 1)
	for _, tool := range tools {
		if tool.Name == targetName {
			filtered = append(filtered, tool)
		}
	}
	if len(filtered) == 0 {
		return nil, "", fmt.Errorf("tool_choice requested unknown tool: %s", targetName)
	}
	return filtered, appendInstruction(instructions, fmt.Sprintf("You must call the %s tool before responding.", targetName)), nil
}

func appendInstruction(instructions string, extra string) string {
	instructions = strings.TrimSpace(instructions)
	extra = strings.TrimSpace(extra)
	if instructions == "" {
		return extra
	}
	if extra == "" {
		return instructions
	}
	return instructions + "\n\n" + extra
}

func normalizeSchemaForCodex(schema any) any {
	switch value := schema.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(value)+2)
		for key, child := range value {
			normalized[key] = normalizeSchemaForCodex(child)
		}
		if schemaType, _ := normalized["type"].(string); schemaType == "object" {
			if _, exists := normalized["additionalProperties"]; !exists {
				normalized["additionalProperties"] = false
			}
			properties, _ := normalized["properties"].(map[string]any)
			if len(properties) > 0 {
				required := make([]any, 0, len(properties))
				keys := make([]string, 0, len(properties))
				for key := range properties {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					required = append(required, key)
				}
				normalized["required"] = required
			}
		}
		return normalized
	case []any:
		normalized := make([]any, len(value))
		for i, child := range value {
			normalized[i] = normalizeSchemaForCodex(child)
		}
		return normalized
	default:
		return schema
	}
}

func extractMessageText(message openai.ChatCompletionMessage) string {
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	parts := make([]string, 0, len(message.MultiContent))
	for _, part := range message.MultiContent {
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (c *Client) loadAccessToken() (string, error) {
	_, accessToken, err := c.loadAuthFile()
	if err != nil {
		return "", err
	}
	return accessToken, nil
}

func (c *Client) AuthStatus() string {
	if _, _, err := c.loadAuthFile(); err != nil {
		return fmt.Sprintf("missing (%v)", err)
	}
	return "available"
}

func (c *Client) loadAuthFile() (string, string, error) {
	codexHome := strings.TrimSpace(c.codexHome)
	if codexHome == "" {
		codexHome = strings.TrimSpace(os.Getenv("CODEX_HOME"))
	}
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("resolve user home for codex auth: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	authPath := filepath.Join(codexHome, "auth.json")
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return authPath, "", fmt.Errorf("read codex auth file %s: %w", authPath, err)
	}
	var auth codexAuthFile
	if err := json.Unmarshal(raw, &auth); err != nil {
		return authPath, "", fmt.Errorf("parse codex auth file %s: %w", authPath, err)
	}
	if auth.AuthMode != "chatgpt" {
		return authPath, "", fmt.Errorf("unsupported codex auth mode %q", auth.AuthMode)
	}
	accessToken := strings.TrimSpace(auth.Tokens.AccessToken)
	if accessToken == "" {
		return authPath, "", fmt.Errorf("codex auth file %s does not contain an access token", authPath)
	}
	return authPath, accessToken, nil
}

func (c *Client) refreshAccessToken(ctx context.Context, authPath string) (string, error) {
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return "", fmt.Errorf("read codex auth file %s: %w", authPath, err)
	}
	var auth codexAuthFile
	if err := json.Unmarshal(raw, &auth); err != nil {
		return "", fmt.Errorf("parse codex auth file %s: %w", authPath, err)
	}
	refreshToken := strings.TrimSpace(auth.Tokens.RefreshToken)
	if refreshToken == "" {
		return "", fmt.Errorf("codex auth file %s does not contain a refresh token", authPath)
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", codexClientID)

	refreshURL := strings.TrimRight(c.authBaseURL, "/") + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build codex token refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send codex token refresh request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read codex token refresh response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("codex token refresh error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var tokenResponse codexTokenResponse
	if err := json.Unmarshal(respBody, &tokenResponse); err != nil {
		return "", fmt.Errorf("parse codex token refresh response: %w", err)
	}
	accessToken := strings.TrimSpace(tokenResponse.AccessToken)
	if accessToken == "" {
		return "", fmt.Errorf("codex token refresh response did not contain an access token")
	}
	newRefreshToken := strings.TrimSpace(tokenResponse.RefreshToken)
	if newRefreshToken == "" {
		newRefreshToken = refreshToken
	}
	if err := persistRefreshedAuthFile(authPath, raw, accessToken, newRefreshToken); err != nil {
		return "", err
	}
	return accessToken, nil
}

func persistRefreshedAuthFile(authPath string, raw []byte, accessToken string, refreshToken string) error {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("parse codex auth file %s: %w", authPath, err)
	}
	tokens, _ := payload["tokens"].(map[string]any)
	if tokens == nil {
		tokens = map[string]any{}
		payload["tokens"] = tokens
	}
	tokens["access_token"] = accessToken
	tokens["refresh_token"] = refreshToken
	payload["last_refresh"] = time.Now().UTC().Format(time.RFC3339Nano)

	updated, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal codex auth file %s: %w", authPath, err)
	}
	updated = append(updated, '\n')
	info, err := os.Stat(authPath)
	mode := os.FileMode(0o600)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(authPath, updated, mode); err != nil {
		return fmt.Errorf("write codex auth file %s: %w", authPath, err)
	}
	return nil
}

func parseSSE(body io.Reader) (openai.ChatCompletionResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	response := openai.ChatCompletionResponse{}
	assistantText := ""
	toolCalls := make([]openai.ToolCall, 0, 1)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event codexEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			return openai.ChatCompletionResponse{}, fmt.Errorf("decode codex event: %w", err)
		}
		switch event.Type {
		case "response.created":
			response.ID = event.Response.ID
			response.Object = "chat.completion"
			response.Created = event.Response.CreatedAt
			response.Model = event.Response.Model
			log.Printf("[openaicodex] response created id=%s model=%s", response.ID, response.Model)
		case "response.output_item.done":
			switch event.Item.Type {
			case "message":
				for _, content := range event.Item.Content {
					if content.Type == "output_text" {
						assistantText += content.Text
					}
				}
				log.Printf("[openaicodex] response message chunk content_len=%d", len(assistantText))
			case "function_call":
				toolCalls = append(toolCalls, openai.ToolCall{
					ID:   event.Item.CallID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      event.Item.Name,
						Arguments: event.Item.Arguments,
					},
				})
				log.Printf("[openaicodex] response tool_call name=%s call_id=%s", event.Item.Name, event.Item.CallID)
			}
		case "response.completed":
			if event.Response.Usage != nil {
				response.Usage = openai.Usage{
					PromptTokens:     event.Response.Usage.InputTokens,
					CompletionTokens: event.Response.Usage.OutputTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				}
			}
			if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
				return openai.ChatCompletionResponse{}, fmt.Errorf("codex completed with error: %s", event.Error.Message)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("read codex event stream: %w", err)
	}
	if response.ID == "" {
		return openai.ChatCompletionResponse{}, fmt.Errorf("codex response stream did not include response.created")
	}

	choice := openai.ChatCompletionChoice{Index: 0}
	if len(toolCalls) > 0 && strings.TrimSpace(assistantText) == "" {
		choice.FinishReason = openai.FinishReasonToolCalls
		choice.Message = openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			ToolCalls: toolCalls,
		}
		log.Printf("[openaicodex] completion finished reason=tool_calls tool_calls=%d", len(toolCalls))
	} else {
		choice.FinishReason = openai.FinishReasonStop
		choice.Message = openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleAssistant,
			Content: assistantText,
		}
		log.Printf("[openaicodex] completion finished reason=stop content_len=%d", len(assistantText))
	}
	response.Choices = []openai.ChatCompletionChoice{choice}
	return response, nil
}
