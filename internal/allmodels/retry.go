package allmodels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// observedProvider accepts only an unmodified, bounded ASCII upstream ID.
// A missing/unknown identifier cannot prove an upstream switch. In particular,
// do not sanitize a label and then use the repaired label as a routing ID.
func (c *Client) observedProvider(data []byte) string {
	var wire struct {
		Provider string `json:"provider"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return ""
	}
	id := wire.Provider
	if len(id) == 0 || len(id) > 96 || c.apiKey != "" && strings.Contains(id, c.apiKey) {
		return ""
	}
	for i, ch := range []byte(id) {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		if i > 0 && (ch == '_' || ch == '-') {
			continue
		}
		return ""
	}
	switch strings.ToLower(id) {
	case "unknown", "none", "null", "undefined":
		return ""
	}
	return id
}

func nonNull(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// Ignore absent/null/blank refusals only. Unexpected refusal shapes fail closed.
func hasRefusal(raw json.RawMessage) bool {
	if !nonNull(raw) {
		return false
	}
	var text string
	return json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) != ""
}

func hasSafetySignal(fields map[string]json.RawMessage) bool {
	// encoding/json matches struct tags case-insensitively, including Unicode
	// simple folds. Inspect every matching key rather than overwriting folded
	// duplicates: a blank variant must not hide a nonempty refusal.
	for key, raw := range fields {
		if strings.EqualFold(key, "refusal") && hasRefusal(raw) {
			return true
		}
		for _, name := range []string{"content_filter", "content_filter_results", "prompt_filter_results", "blocked", "safety", "moderation", "finish_reason"} {
			if strings.EqualFold(key, name) && nonNull(raw) {
				return true
			}
		}
	}
	return false
}

// Called only after checkEmptyCompletion. Permit a single terminal stop choice
// with no tool/function payload or safety signal. Zero choices (no terminal
// reason), unknown reasons, malformed tools and ambiguous multi-choice responses
// are deliberately not retryable. Reasoning remains private, never final text.
func (c *Client) emptyRetryProvider(data []byte) string {
	provider := c.observedProvider(data)
	if provider == "" {
		return ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || hasSafetySignal(root) {
		return ""
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil || len(choices) != 1 {
		return ""
	}
	choice := choices[0]
	var reason string
	if json.Unmarshal(choice["finish_reason"], &reason) != nil || reason != "stop" {
		return ""
	}
	// Check safety metadata at both levels without treating the required stop
	// reason itself as a safety signal.
	delete(choice, "finish_reason")
	if hasSafetySignal(choice) {
		return ""
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(choice["message"], &message) != nil || message == nil || hasSafetySignal(message) {
		return ""
	}
	for key, raw := range message {
		if (strings.EqualFold(key, "tool_calls") || strings.EqualFold(key, "function_call")) && nonNull(raw) {
			return ""
		}
		if strings.EqualFold(key, "content") && nonNull(raw) {
			var text string
			if json.Unmarshal(raw, &text) != nil || strings.TrimSpace(text) != "" {
				return ""
			}
		}
	}
	return provider
}

func (c *Client) retryEmptyCompletion(ctx context.Context, body map[string]any, provider string) (openai.ChatCompletionResponse, error) {
	var out openai.ChatCompletionResponse
	// Reuse the already-encoded request: completed host-tool history, hosted
	// search, model and all other parameters stay unchanged. No tools run here.
	body["provider"] = map[string]any{"zdr": true, "ignore": []string{provider}}
	log.Printf("[allmodels] retry empty completion provider=%s attempt=2", diagnosticLabel(provider, c.apiKey))
	data, err := c.exchange(ctx, body)
	if err != nil {
		return out, fmt.Errorf("%w: alternate upstream failed: %w", ErrEmptyCompletion, err)
	}
	if json.Unmarshal(data, &out) != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("%w: decode alternate upstream response", ErrEmptyCompletion)
	}
	// Report every empty attempt, even if the router violated the exclusion.
	if err = c.checkEmptyCompletion(data, out); err != nil {
		return openai.ChatCompletionResponse{}, err
	}
	alternate := c.observedProvider(data)
	if alternate == "" || strings.EqualFold(alternate, provider) {
		return openai.ChatCompletionResponse{}, fmt.Errorf("%w: alternate upstream not verified", ErrEmptyCompletion)
	}
	log.Printf("[allmodels] recovery succeeded provider=%s id=%s attempt=2", diagnosticLabel(alternate, c.apiKey), diagnosticLabel(out.ID, c.apiKey))
	return out, nil
}
