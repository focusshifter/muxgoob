// Package allmodels provides the explicitly selected AllModels chat route.
// It never borrows credentials or calls another router's search service.
package allmodels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const BaseURL = "https://api.allmodels.io/oai"
const requestTimeout = 120 * time.Second
const maxResponseBytes = 4 << 20

type Option func(*Client)
type Client struct {
	apiKey, baseURL string
	httpClient      *http.Client
}

func NewClient(apiKey string, options ...Option) *Client {
	c := &Client{apiKey: apiKey, baseURL: BaseURL, httpClient: &http.Client{Timeout: requestTimeout}}
	for _, option := range options {
		option(c)
	}
	return c
}

// WithBaseURL sets the API root, including /oai when applicable.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func (c *Client) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	// One budget includes both attempts, not a fresh timeout per upstream.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var out openai.ChatCompletionResponse
	if strings.TrimSpace(c.apiKey) == "" {
		return out, errors.New("allmodels: missing API key")
	}
	if req.Stream {
		return out, errors.New("allmodels: CreateChatCompletion does not support stream=true")
	}
	online := strings.HasSuffix(req.Model, ":online")
	if online {
		req.Model = strings.TrimSuffix(req.Model, ":online")
	}
	if strings.TrimSpace(req.Model) == "" || req.Model == "online:auto" {
		return out, errors.New("allmodels: select an explicit nonempty model")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return out, errors.New("allmodels: encode chat request")
	}
	if !unambiguousJSON(encoded) {
		return out, errors.New("allmodels: ambiguous chat request")
	}
	var body map[string]any
	if err = json.Unmarshal(encoded, &body); err != nil {
		return out, errors.New("allmodels: encode chat request")
	}
	// Documented at /llm/docs/providers: ZDR filters every eligible route,
	// including upstream capacity failovers. Never relax this on provider errors.
	body["provider"] = map[string]any{"zdr": true}
	if online {
		body["web_search_options"] = map[string]any{"search_context_size": "medium"}
	}
	data, err := c.exchange(ctx, body)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("%w: decode provider response", ErrEmptyCompletion)
	}
	if err = c.normalizeStopToolCalls(body, data, &out); err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	if err = c.checkEmptyCompletion(data, out); err != nil {
		provider := c.emptyRetryProvider(data)
		if provider == "" {
			return openai.ChatCompletionResponse{}, err
		}
		if ctx.Err() != nil {
			return openai.ChatCompletionResponse{}, fmt.Errorf("%w: %w", err, ctx.Err())
		}
		return c.retryEmptyCompletion(ctx, body, provider)
	}
	return out, nil
}

// exchange is bounded and intentionally excludes upstream bodies, URLs and
// transport errors, which may echo secrets, private prompts or image data.
// Each exchange makes exactly one POST. CreateChatCompletion owns the shared
// deadline and the sole, narrowly gated empty-completion retry.
func (c *Client) exchange(ctx context.Context, body any) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("allmodels: encode request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("allmodels: invalid API URL")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("allmodels: request canceled: %w", ctx.Err())
		}
		return nil, errors.New("allmodels: HTTP transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("allmodels: provider HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("allmodels: read provider response")
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("allmodels: response exceeds size limit")
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errors.New("allmodels: decode provider response")
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return nil, errors.New("allmodels: provider returned an error")
	}
	return data, nil
}
