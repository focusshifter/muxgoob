package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

func marshalJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"error":"failed to encode tool result"}`
	}

	return string(data)
}

func toolError(name, message string) string {
	return marshalJSON(map[string]string{
		"tool":  name,
		"error": message,
	})
}

func summarizeToolResult(result string) string {
	if strings.TrimSpace(result) == "" {
		return "empty result"
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		return fmt.Sprintf("non-json result len=%d", len(result))
	}

	if errMessage, ok := payload["error"].(string); ok && errMessage != "" {
		return fmt.Sprintf("error=%q", errMessage)
	}

	parts := make([]string, 0, 3)
	if count, ok := payload["count"]; ok {
		parts = append(parts, fmt.Sprintf("count=%v", count))
	}
	if users, ok := payload["users"].([]any); ok {
		parts = append(parts, fmt.Sprintf("users=%d", len(users)))
	}
	if results, ok := payload["results"].([]any); ok {
		parts = append(parts, fmt.Sprintf("results=%d", len(results)))
	}
	if notInChat, ok := payload["not_in_chat"].([]any); ok && len(notInChat) > 0 {
		parts = append(parts, fmt.Sprintf("not_in_chat=%d", len(notInChat)))
	}
	if noFacts, ok := payload["no_facts"].([]any); ok && len(noFacts) > 0 {
		parts = append(parts, fmt.Sprintf("no_facts=%d", len(noFacts)))
	}

	if len(parts) == 0 {
		return fmt.Sprintf("json result len=%d", len(result))
	}

	return strings.Join(parts, " ")
}
