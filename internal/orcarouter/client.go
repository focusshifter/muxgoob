// Package orcarouter adapts OrcaRouter's native online protocols to go-openai.
// Model IDs are opaque: only a terminal :online suffix is removed.
package orcarouter

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

const defaultBaseURL = "https://api.orcarouter.ai/v1"
const maxResponseBytes = 4 << 20
const requestTimeout = 120 * time.Second

type Option func(*Client)
type Client struct {
	apiKey, baseURL string
	httpClient      *http.Client
}

func NewClient(apiKey string, options ...Option) *Client {
	c := &Client{apiKey: apiKey, baseURL: defaultBaseURL, httpClient: &http.Client{Timeout: requestTimeout}}
	for _, o := range options {
		o(c)
	}
	return c
}

// WithBaseURL sets the API root, including /v1 when applicable.
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
	var zero openai.ChatCompletionResponse
	if strings.TrimSpace(c.apiKey) == "" {
		return zero, errors.New("orcarouter: missing API key")
	}
	if req.Stream {
		return zero, errors.New("orcarouter: CreateChatCompletion does not support stream=true")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if req.Model == "online:auto" {
		return zero, errors.New("orcarouter: unsupported online:auto; select an explicit model")
	}
	if !strings.HasSuffix(req.Model, ":online") {
		return c.chat(ctx, req, nil)
	}
	req.Model = strings.TrimSuffix(req.Model, ":online")
	family := onlineFamily(req.Model)
	switch family {
	case "responses":
		return c.responses(ctx, req)
	case "anthropic":
		return c.chat(ctx, req, map[string]any{"web_search_options": map[string]any{"search_context_size": "medium"}})
	case "gemini":
		for _, t := range req.Tools {
			if t.Function != nil && t.Function.Name == "googleSearch" {
				return zero, errors.New("orcarouter: reserved googleSearch tool name conflict")
			}
		}
		// Do not mutate the caller's tools backing array.
		b, err := json.Marshal(req)
		if err != nil {
			return zero, errors.New("orcarouter: encode chat request")
		}
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		tools, _ := body["tools"].([]any)
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": "googleSearch"}})
		body["tools"] = tools
		err = c.exchange(ctx, http.MethodPost, "/chat/completions", body, &zero)
		return zero, err
	case "generic":
		return c.genericOnline(ctx, req)
	default:
		return zero, fmt.Errorf("orcarouter: unsupported :online model family %q; choose an explicit supported model", req.Model)
	}
}
func onlineFamily(model string) string {
	switch {
	case strings.HasPrefix(model, "grok/grok-"), strings.HasPrefix(model, "x-ai/grok-"), strings.HasPrefix(model, "openai/"):
		return "responses"
	case strings.HasPrefix(model, "anthropic/claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "google/gemini-"):
		return "gemini"
	case strings.HasPrefix(model, "deepseek/"), strings.HasPrefix(model, "qwen/"), strings.HasPrefix(model, "moonshot/"), strings.HasPrefix(model, "kimi/"), strings.HasPrefix(model, "minimax/"):
		return "generic"
	default:
		return ""
	}
}
func (c *Client) chat(ctx context.Context, req openai.ChatCompletionRequest, extra map[string]any) (out openai.ChatCompletionResponse, err error) {
	if extra == nil {
		err = c.exchange(ctx, http.MethodPost, "/chat/completions", req, &out)
		return
	}
	b, e := json.Marshal(req)
	if e != nil {
		return out, errors.New("orcarouter: encode chat request")
	}
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	for k, v := range extra {
		body[k] = v
	}
	err = c.exchange(ctx, http.MethodPost, "/chat/completions", body, &out)
	return
}

// exchange deliberately excludes upstream bodies/URLs and transport error strings
// from errors: they can echo credentials, private prompts, or inline image data.
// No automatic retries, including paid standalone tool runs.
func (c *Client) exchange(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errors.New("orcarouter: encode request")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return errors.New("orcarouter: invalid API URL")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	// Never follow redirects with a provider key, even with an injected client.
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("orcarouter: request canceled: %w", ctx.Err())
		}
		return errors.New("orcarouter: HTTP transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("orcarouter: provider HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return errors.New("orcarouter: read provider response")
	}
	if len(data) > maxResponseBytes {
		return errors.New("orcarouter: response exceeds size limit")
	}
	var envelope struct {
		Error  json.RawMessage `json:"error"`
		Status string          `json:"status"`
	}
	if json.Unmarshal(data, &envelope) == nil && len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		if envelope.Status != "" {
			return fmt.Errorf("orcarouter: provider response status %q", safeStatus(envelope.Status))
		}
		return errors.New("orcarouter: provider returned an error")
	}
	if err = json.Unmarshal(data, out); err != nil {
		return errors.New("orcarouter: decode provider response")
	}
	return nil
}
