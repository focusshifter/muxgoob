package tools

import "encoding/json"

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
