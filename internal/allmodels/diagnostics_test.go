package allmodels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func diagnosticFixture(t *testing.T, body, key string) (openai.ChatCompletionResponse, error, string) {
	t.Helper()
	var logs bytes.Buffer
	oldWriter, oldFlags, oldPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() { log.SetOutput(oldWriter); log.SetFlags(oldFlags); log.SetPrefix(oldPrefix) }()
	calls := 0
	c := NewClient(key, WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["provider"].(map[string]any)["zdr"] != true {
			t.Fatal("ZDR changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	out, err := c.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "deepseek/deepseek-v4.1-flash-abliterated",
		Messages: []openai.ChatCompletionMessage{{Role: "user", Content: "PRIVATE_NEUTRAL_PROMPT"}},
	})
	if calls != 1 {
		t.Fatalf("calls=%d; expected no retries", calls)
	}
	return out, err, logs.String()
}

func TestEmptyCompletionDiagnostics(t *testing.T) {
	tool := `[{"id":"PRIVATE_TOOL_ID","type":"function","function":{"name":"PRIVATE_TOOL_NAME","arguments":"PRIVATE_TOOL_ARGS"}}]`
	for _, tc := range []struct {
		name, reason, message string
		empty                 bool
	}{
		{"null", "stop", `{"content":null}`, true},
		{"empty", "stop", `{"content":""}`, true},
		{"whitespace", "stop", `{"content":" \t\n"}`, true},
		{"length", "length", `{"content":""}`, true},
		{"missing", "stop", `{}`, true},
		{"reasoning_content", "stop", `{"reasoning_content":"PRIVATE_REASONING_CONTENT"}`, true},
		{"reasoning", "stop", `{"reasoning":"PRIVATE_REASONING"}`, true},
		{"refusal", "stop", `{"refusal":"PRIVATE_REFUSAL"}`, true},
		{"malformed_stop_tools", "stop", `{"tool_calls":` + tool + `}`, true},
		{"malformed_length_tools", "length", `{"tool_calls":` + tool + `}`, true},
		{"malformed_function", "stop", `{"function_call":{"name":"PRIVATE_FUNCTION","arguments":"PRIVATE_ARGS"}}`, true},
		{"ordinary_tools", "tool_calls", `{"tool_calls":` + tool + `}`, false},
		{"tools_without_tools", "tool_calls", `{}`, true},
		{"text", "stop", `{"content":"PRIVATE_VALID_COMPLETION"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"id":"chatcmpl-fixture","choices":[{"finish_reason":"` + tc.reason + `","message":` + tc.message + `}]}`
			out, err, logs := diagnosticFixture(t, body, "fixture-key")
			if tc.empty {
				if !errors.Is(err, ErrEmptyCompletion) {
					t.Fatalf("expected empty completion sentinel, got %v", err)
				}
				if len(out.Choices) != 0 {
					t.Fatal("error response exposes malformed tool commands")
				}
				if !strings.Contains(logs, `"finish_reason":"`+tc.reason+`"`) {
					t.Fatalf("missing diagnostic finish reason: %s", logs)
				}
				var shape struct {
					Content   diagnosticField `json:"content"`
					ToolCalls diagnosticField `json:"tool_calls"`
				}
				if e := json.Unmarshal([]byte(strings.TrimPrefix(logs, "[allmodels] empty completion ")), &shape); e != nil {
					t.Fatal(e)
				}
				if tc.name == "null" && (!shape.Content.Present || !shape.Content.Null) {
					t.Fatal("null content presence lost")
				}
				if tc.name == "empty" && (!shape.Content.Present || shape.Content.Null || shape.Content.Bytes != 0) {
					t.Fatal("empty string shape lost")
				}
				if tc.name == "missing" && shape.Content.Present {
					t.Fatal("missing content incorrectly present")
				}
				if strings.HasSuffix(tc.name, "_tools") && strings.HasPrefix(tc.name, "malformed_") && shape.ToolCalls.Count != 1 {
					t.Fatal("malformed terminal tool shape lost")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(out.Choices) != 1 {
					t.Fatal("lost choice")
				}
				if logs != "" {
					t.Fatalf("unexpected diagnostics: %s", logs)
				}
			}
			if strings.Contains(logs, "PRIVATE_") {
				t.Fatalf("private fixture leaked: %s", logs)
			}
		})
	}
}

func TestEmptyCompletionDiagnosticPrivacyAndMetadata(t *testing.T) {
	key := "fixture-secret/KEY:123"
	body := `{"id":"chat-` + key + `\nINJECT","provider":"upstream-` + key + `\tPRIVATE","choices":[{"finish_reason":"stop-` + key + `\nINJECT","message":{"role":"PRIVATE_ROLE","content":" \n","reasoning_content":"PRIVATE_THOUGHT","reasoning":["PRIVATE_ARRAY"],"refusal":"PRIVATE_REFUSAL","tool_calls":[{"id":"PRIVATE_ID","function":{"name":"PRIVATE_FUNCTION","arguments":"PRIVATE_ARGS"}}],"function_call":{"name":"PRIVATE_LEGACY","arguments":"PRIVATE_ARGS"},"PRIVATE_UNKNOWN_FIELD":"PRIVATE_UNKNOWN_VALUE"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":3,"audio_tokens":2},"completion_tokens_details":{"reasoning_tokens":5,"audio_tokens":1}},"PRIVATE_TOP_FIELD":"PRIVATE_TOP_VALUE"}`
	_, err, logs := diagnosticFixture(t, body, key)
	if err == nil {
		t.Fatal("expected empty completion error")
	}
	for _, secret := range []string{key, "PRIVATE_NEUTRAL_PROMPT", "PRIVATE_ROLE", "PRIVATE_THOUGHT", "PRIVATE_ARRAY", "PRIVATE_REFUSAL", "PRIVATE_ID", "PRIVATE_FUNCTION", "PRIVATE_ARGS", "PRIVATE_LEGACY", "PRIVATE_UNKNOWN_FIELD", "PRIVATE_UNKNOWN_VALUE", "PRIVATE_TOP_FIELD", "PRIVATE_TOP_VALUE"} {
		if strings.Contains(logs, secret) || strings.Contains(err.Error(), secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
	if strings.Count(logs, "\n") != 1 {
		t.Fatalf("log injection: %q", logs)
	}
	var d map[string]any
	const prefix = "[allmodels] empty completion "
	if !strings.HasPrefix(logs, prefix) {
		t.Fatalf("missing log: %s", logs)
	}
	if e := json.Unmarshal([]byte(strings.TrimPrefix(logs, prefix)), &d); e != nil {
		t.Fatal(e)
	}
	for field, want := range map[string]float64{"choice_count": 1, "prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18, "cached_tokens": 3, "prompt_audio_tokens": 2, "reasoning_tokens": 5, "completion_audio_tokens": 1} {
		if d[field] != want {
			t.Fatalf("%s=%v want %v", field, d[field], want)
		}
	}
	for _, field := range []string{"role", "content", "reasoning_content", "reasoning", "refusal", "tool_calls", "function_call"} {
		summary, ok := d[field].(map[string]any)
		if !ok || summary["present"] != true {
			t.Fatalf("missing %s presence: %v", field, d[field])
		}
	}
	if d["tool_calls"].(map[string]any)["count"] != float64(1) {
		t.Fatal("lost tool shape")
	}
	if d["reasoning_content"].(map[string]any)["bytes"] != float64(len("PRIVATE_THOUGHT")) {
		t.Fatal("lost reasoning length")
	}
}

func TestZeroChoicesDiagnosticReturnsEmptyError(t *testing.T) {
	out, err, logs := diagnosticFixture(t, `{"id":"zero-fixture","choices":[]}`, "fixture-key")
	if !errors.Is(err, ErrEmptyCompletion) || len(out.Choices) != 0 {
		t.Fatalf("zero choices must be an empty-completion error: %v", err)
	}
	if !strings.Contains(logs, `"choice_count":0`) {
		t.Fatalf("missing zero-choice diagnostic: %s", logs)
	}
}

func TestDiagnosticLabelRedactionBeforeSanitizeAndBound(t *testing.T) {
	for _, key := range []string{"fixture-key/123", "fixture\nkey\t123", "fixture-ключ-123"} {
		for _, prefix := range []string{"chat-", strings.Repeat("x", 90)} {
			got := diagnosticLabel(prefix+key+"\nunsafe\"suffix", key)
			if len(got) > 96 || strings.ContainsAny(got, "\n\t\r\"") || strings.Contains(got, key) {
				t.Fatalf("unsafe label: %q", got)
			}
			if strings.Contains(got, "fixture") {
				t.Fatalf("partial secret escaped pre-truncation redaction: %q", got)
			}
		}
	}
}

func TestDiagnosticMetadataBounded(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"id": strings.Repeat("x", 4096), "provider": strings.Repeat("y", 4096), "choices": []any{map[string]any{"finish_reason": strings.Repeat("z", 4096), "message": map[string]any{"content": ""}}}})
	_, err, logs := diagnosticFixture(t, string(body), "fixture-key")
	if err == nil {
		t.Fatal("expected empty completion error")
	}
	if len(logs) > 2048 {
		t.Fatalf("unbounded metadata: %d bytes", len(logs))
	}
}
