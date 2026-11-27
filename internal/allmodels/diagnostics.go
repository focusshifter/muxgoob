package allmodels

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// ErrEmptyCompletion identifies a response without usable final text or an
// ordinary tool_calls continuation. It does not identify the upstream cause.
var ErrEmptyCompletion = errors.New("allmodels: empty completion")

// diagnosticField contains shape only: never field values or nested names.
// Bytes counts decoded bytes for strings, JSON bytes otherwise. Count is the
// number of array elements or object members; null and absent remain distinct.
type diagnosticField struct {
	Present bool `json:"present"`
	Null    bool `json:"null"`
	Bytes   int  `json:"bytes"`
	Count   int  `json:"count"`
}

func summarizeField(raw json.RawMessage) diagnosticField {
	s := diagnosticField{Present: len(raw) != 0}
	if !s.Present {
		return s
	}
	raw = bytes.TrimSpace(raw)
	s.Null = bytes.Equal(raw, []byte("null"))
	if s.Null {
		return s
	}
	s.Bytes = len(raw)
	var text string
	if json.Unmarshal(raw, &text) == nil {
		s.Bytes = len(text)
		return s
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil {
		s.Count = len(array)
		return s
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		s.Count = len(object)
	}
	return s
}

// diagnosticLabel redacts before filtering/truncation so a secret crossing the
// bound or containing punctuation/control characters cannot be echoed. Only
// bounded ASCII label characters survive; no log lines can be injected.
func diagnosticLabel(value, apiKey string) string {
	if apiKey != "" {
		value = strings.ReplaceAll(value, apiKey, "[redacted]")
	}
	var b strings.Builder
	for _, r := range value {
		if b.Len() >= 96 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-/:[]", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	// Filtering must not accidentally assemble a key from disjoint characters.
	label := b.String()
	if apiKey != "" {
		label = strings.ReplaceAll(label, apiKey, "[redacted]")
	}
	if len(label) > 96 {
		label = label[:96]
	}
	return label
}

// checkEmptyCompletion runs only after the SDK decode. Raw fields are inspected
// solely for allowlisted shape metadata the SDK otherwise discards. No raw
// response, arbitrary names, prompt, text, headers or credentials reach logs.
func (c *Client) checkEmptyCompletion(data []byte, out openai.ChatCompletionResponse) error {
	if len(out.Choices) > 0 {
		first := out.Choices[0]
		if strings.TrimSpace(first.Message.Content) != "" {
			return nil
		}
		if first.FinishReason == openai.FinishReasonToolCalls && len(first.Message.ToolCalls) > 0 {
			return nil
		}
	}
	return c.logUnusableCompletion(data, out)
}

// logUnusableCompletion also reports malformed tool continuations with text;
// they must not pass checkEmptyCompletion's ordinary text acceptance shortcut.
func (c *Client) logUnusableCompletion(data []byte, out openai.ChatCompletionResponse) error {
	var wire struct {
		Provider json.RawMessage `json:"provider"`
		Choices  []struct {
			Message json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	// SDK decoding already succeeded. A differently shaped optional provider must
	// not prevent the empty-response check or cause unsafe logging on an error.
	_ = json.Unmarshal(data, &wire)
	var provider string
	_ = json.Unmarshal(wire.Provider, &provider)
	d := struct {
		ID                    string          `json:"id"`
		Provider              string          `json:"provider"`
		FinishReason          string          `json:"finish_reason"`
		ChoiceCount           int             `json:"choice_count"`
		Message               diagnosticField `json:"message"`
		Role                  diagnosticField `json:"role"`
		Content               diagnosticField `json:"content"`
		ReasoningContent      diagnosticField `json:"reasoning_content"`
		Reasoning             diagnosticField `json:"reasoning"`
		Refusal               diagnosticField `json:"refusal"`
		ToolCalls             diagnosticField `json:"tool_calls"`
		FunctionCall          diagnosticField `json:"function_call"`
		PromptTokens          int             `json:"prompt_tokens"`
		CompletionTokens      int             `json:"completion_tokens"`
		TotalTokens           int             `json:"total_tokens"`
		CachedTokens          int             `json:"cached_tokens"`
		PromptAudioTokens     int             `json:"prompt_audio_tokens"`
		ReasoningTokens       int             `json:"reasoning_tokens"`
		CompletionAudioTokens int             `json:"completion_audio_tokens"`
	}{
		ID: diagnosticLabel(out.ID, c.apiKey), Provider: diagnosticLabel(provider, c.apiKey),
		ChoiceCount: len(out.Choices), PromptTokens: out.Usage.PromptTokens,
		CompletionTokens: out.Usage.CompletionTokens, TotalTokens: out.Usage.TotalTokens,
	}
	if detail := out.Usage.PromptTokensDetails; detail != nil {
		d.CachedTokens, d.PromptAudioTokens = detail.CachedTokens, detail.AudioTokens
	}
	if detail := out.Usage.CompletionTokensDetails; detail != nil {
		d.ReasoningTokens, d.CompletionAudioTokens = detail.ReasoningTokens, detail.AudioTokens
	}
	if len(out.Choices) > 0 {
		d.FinishReason = diagnosticLabel(string(out.Choices[0].FinishReason), c.apiKey)
	}
	if len(wire.Choices) > 0 {
		message := wire.Choices[0].Message
		d.Message = summarizeField(message)
		var fields struct {
			Role             json.RawMessage `json:"role"`
			Content          json.RawMessage `json:"content"`
			ReasoningContent json.RawMessage `json:"reasoning_content"`
			Reasoning        json.RawMessage `json:"reasoning"`
			Refusal          json.RawMessage `json:"refusal"`
			ToolCalls        json.RawMessage `json:"tool_calls"`
			FunctionCall     json.RawMessage `json:"function_call"`
		}
		_ = json.Unmarshal(message, &fields)
		d.Role, d.Content = summarizeField(fields.Role), summarizeField(fields.Content)
		d.ReasoningContent, d.Reasoning = summarizeField(fields.ReasoningContent), summarizeField(fields.Reasoning)
		d.Refusal, d.ToolCalls = summarizeField(fields.Refusal), summarizeField(fields.ToolCalls)
		d.FunctionCall = summarizeField(fields.FunctionCall)
	}
	encoded, _ := json.Marshal(d) // Only strings, bools and integers: cannot fail.
	log.Printf("[allmodels] empty completion %s", encoded)
	return ErrEmptyCompletion
}
