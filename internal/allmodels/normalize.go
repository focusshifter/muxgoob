package allmodels

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"unicode"

	openai "github.com/sashabaranov/go-openai"
)

// normalizeStopToolCalls only repairs the known terminal-stop compatibility
// mismatch. It never executes tools or changes text, arguments or tool IDs.
// Invalid stop/tool mixtures fail before the ordinary text/empty acceptance path.
func (c *Client) normalizeStopToolCalls(body map[string]any, data []byte, out *openai.ChatCompletionResponse) error {
	unusable := func() error {
		_ = c.logUnusableCompletion(data, *out)
		return fmt.Errorf("%w: unusable tool continuation", ErrEmptyCompletion)
	}
	// A map decode alone loses duplicate keys, including a safety field overwritten
	// by null. Reject ambiguous JSON before inspecting either raw or SDK fields.
	if !unambiguousJSON(data) {
		return unusable()
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return unusable()
	}
	var choices []map[string]json.RawMessage
	for key, raw := range root {
		if strings.EqualFold(key, "choices") {
			_ = json.Unmarshal(raw, &choices)
		}
	}
	candidate := false
	for _, choice := range choices {
		var reason string
		for key, raw := range choice {
			if strings.EqualFold(key, "finish_reason") {
				_ = json.Unmarshal(raw, &reason)
			}
		}
		if reason != "stop" {
			continue
		}
		for key, raw := range choice {
			if !strings.EqualFold(key, "message") {
				continue
			}
			var message map[string]json.RawMessage
			_ = json.Unmarshal(raw, &message)
			for field, value := range message {
				if (strings.EqualFold(field, "tool_calls") || strings.EqualFold(field, "function_call")) && nonNull(value) {
					candidate = true
				}
			}
		}
	}
	if !candidate {
		return nil
	}
	if len(choices) != 1 || len(out.Choices) != 1 || out.Choices[0].FinishReason != openai.FinishReasonStop || hasSafetySignal(root) {
		return unusable()
	}
	choice := choices[0]
	var reason string
	if json.Unmarshal(choice["finish_reason"], &reason) != nil || reason != "stop" {
		return unusable()
	}
	// Ignore precisely the required canonical stop reason, not case variants or
	// safety metadata at another level. The duplicate guard already rejected aliases.
	delete(choice, "finish_reason")
	if hasSafetySignal(choice) {
		return unusable()
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(choice["message"], &message) != nil || message == nil || hasSafetySignal(message) {
		return unusable()
	}
	for key, raw := range message {
		if strings.EqualFold(key, "function_call") && nonNull(raw) {
			return unusable()
		}
	}
	var rawCalls []json.RawMessage
	if json.Unmarshal(message["tool_calls"], &rawCalls) != nil || len(rawCalls) == 0 {
		return unusable()
	}
	calls := out.Choices[0].Message.ToolCalls
	if len(calls) != len(rawCalls) {
		return unusable()
	}
	allowed, forced, ok := permittedRequestTools(body)
	if !ok {
		return unusable()
	}
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.Type != openai.ToolTypeFunction || strings.TrimSpace(call.ID) == "" || ids[call.ID] || strings.TrimSpace(call.Function.Name) == "" || !allowed[call.Function.Name] || forced != "" && call.Function.Name != forced {
			return unusable()
		}
		ids[call.ID] = true
		var arguments map[string]json.RawMessage
		if json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments == nil || !unambiguousJSONScope([]byte(call.Function.Arguments), "data") {
			return unusable()
		}
	}
	out.Choices[0].FinishReason = openai.FinishReasonToolCalls
	log.Printf("[allmodels] normalized tool calls id=%s provider=%s count=%d", diagnosticLabel(out.ID, c.apiKey), diagnosticLabel(c.observedProvider(data), c.apiKey), len(calls))
	return nil
}

// Inspect the actual marshaled request shape: ToolChoice may be a string, SDK
// struct/pointer or user-provided object. Unknown/ambiguous choices fail closed.
func permittedRequestTools(body map[string]any) (map[string]bool, string, bool) {
	allowed := map[string]bool{}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		return nil, "", false
	}
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok || tool["type"] != "function" {
			return nil, "", false
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			return nil, "", false
		}
		name, ok := function["name"].(string)
		if !ok || strings.TrimSpace(name) == "" || allowed[name] {
			return nil, "", false
		}
		allowed[name] = true
	}
	choice, exists := body["tool_choice"]
	if !exists || choice == nil {
		return allowed, "", true
	}
	if text, ok := choice.(string); ok {
		return allowed, "", text == "auto" || text == "required"
	}
	encoded, err := json.Marshal(choice)
	if err != nil || !unambiguousJSON(encoded) {
		return nil, "", false
	}
	object, ok := choice.(map[string]any)
	if !ok || len(object) != 2 || object["type"] != "function" {
		return nil, "", false
	}
	function, ok := object["function"].(map[string]any)
	if !ok || len(function) != 1 {
		return nil, "", false
	}
	name, ok := function["name"].(string)
	return allowed, name, ok && allowed[name]
}

// Fold only SDK protocol-object fields; schemas, metadata and tool arguments
// contain case-sensitive user keys. Exact duplicates remain invalid everywhere.
func unambiguousJSON(data []byte) bool {
	return unambiguousJSONScope(data, "root")
}

func unambiguousJSONScope(data []byte, scope string) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !walkJSON(decoder, 0, scope) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func childJSONScope(scope, key string) string {
	switch scope {
	case "root":
		switch {
		case strings.EqualFold(key, "choices"):
			return "choice_array"
		case strings.EqualFold(key, "messages"):
			return "message_array"
		case strings.EqualFold(key, "tools"):
			return "tool_array"
		case strings.EqualFold(key, "tool_choice"):
			return "tool"
		}
	case "choice":
		if strings.EqualFold(key, "message") {
			return "message"
		}
	case "message":
		if strings.EqualFold(key, "tool_calls") {
			return "tool_array"
		}
		if strings.EqualFold(key, "function_call") {
			return "function"
		}
	case "tool":
		if strings.EqualFold(key, "function") {
			return "function"
		}
	}
	return "data"
}

func walkJSON(decoder *json.Decoder, depth int, scope string) bool {
	if depth > 100 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := token.(string)
			if !ok {
				return false
			}
			folded := key
			if scope != "data" {
				folded = strings.Map(func(r rune) rune {
					min := r
					for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
						if next < min {
							min = next
						}
					}
					return min
				}, key)
			}
			if seen[folded] {
				return false
			}
			seen[folded] = true
			if !walkJSON(decoder, depth+1, childJSONScope(scope, key)) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		elementScope := "data"
		if strings.HasSuffix(scope, "_array") {
			elementScope = strings.TrimSuffix(scope, "_array")
		}
		for decoder.More() {
			if !walkJSON(decoder, depth+1, elementScope) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
