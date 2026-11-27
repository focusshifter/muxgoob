package allmodels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func retryWire(provider, reason, message string) string {
	return `{"provider":` + provider + `,"choices":[{"finish_reason":` + reason + `,"message":` + message + `}]}`
}

var technicalEmpty = retryWire(`"upstream-a"`, `"stop"`, `{"content":" \t","reasoning_content":"PRIVATE_REASONING"}`)
var alternateText = retryWire(`"upstream-b"`, `"stop"`, `{"content":"fixture complete"}`)

func retryRequest() openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{Model: "fixture/model:online", MaxTokens: 1024,
		Messages: []openai.ChatCompletionMessage{
			{Role: "user", MultiContent: []openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: "PRIVATE_NEUTRAL_PROMPT"}, {Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,PRIVATE_IMAGE"}}}},
			{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "fixture-call", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "fixture_marker", Arguments: "{}"}}}},
			{Role: "tool", ToolCallID: "fixture-call", Content: "PRIVATE_COMPLETED_TOOL_RESULT"},
		},
		Tools: []openai.Tool{{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "fixture_marker", Parameters: map[string]any{"type": "object"}}}}, ToolChoice: "none"}
}

func retryFixture(t *testing.T, first, second string, secondStatus int) (openai.ChatCompletionResponse, error, int, string) {
	t.Helper()
	var logs bytes.Buffer
	oldWriter, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() { log.SetOutput(oldWriter); log.SetFlags(oldFlags) }()
	calls := 0
	var original map[string]any
	var deadline time.Time
	req := retryRequest()
	before, _ := json.Marshal(req)
	c := NewClient("fixture-key", WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			t.Fatal("third completion POST")
		}
		if r.URL.String() != BaseURL+"/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Fatal("route/auth changed")
		}
		d, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("missing request budget")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		status, response := 200, first
		if calls == 1 {
			deadline = d
			if time.Until(d) > requestTimeout || time.Until(d) < requestTimeout-time.Second {
				t.Fatal("incorrect request-wide budget")
			}
			original = body
			if !reflect.DeepEqual(body["provider"], map[string]any{"zdr": true}) {
				t.Fatal("wrong initial provider")
			}
		} else {
			if !deadline.Equal(d) {
				t.Fatal("retry resets request budget")
			}
			var envelope struct {
				Provider string `json:"provider"`
			}
			_ = json.Unmarshal([]byte(first), &envelope)
			if !reflect.DeepEqual(body["provider"], map[string]any{"zdr": true, "ignore": []any{envelope.Provider}}) {
				t.Fatalf("wrong retry provider: %v", body["provider"])
			}
			delete(body, "provider")
			delete(original, "provider")
			if !reflect.DeepEqual(body, original) {
				t.Fatal("retry changed model/messages/tools/search or completed tool history")
			}
			status, response = secondStatus, second
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}))
	out, err := c.CreateChatCompletion(context.Background(), req)
	after, _ := json.Marshal(req)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated caller")
	}
	for _, secret := range []string{"fixture-key", "PRIVATE_NEUTRAL_PROMPT", "PRIVATE_REASONING", "PRIVATE_COMPLETED_TOOL_RESULT", "PRIVATE_IMAGE", "PRIVATE_REFUSAL", "PRIVATE_ERROR"} {
		if strings.Contains(logs.String(), secret) || err != nil && strings.Contains(err.Error(), secret) {
			t.Fatalf("private fixture leaked: %s", secret)
		}
	}
	return out, err, calls, logs.String()
}

func TestTechnicalEmptyRetriesDifferentUpstream(t *testing.T) {
	for _, message := range []string{`{"content":""}`, `{"content":null}`, `{"content":" \n\t"}`, `{}`, `{"reasoning_content":"PRIVATE_REASONING"}`, `{"content":"","refusal":null}`, `{"content":"","refusal":" \t"}`, `{"content":"","tool_calls":null,"function_call":null}`} {
		t.Run(message, func(t *testing.T) {
			first := retryWire(`"Upstream-A"`, `"stop"`, message)
			out, err, calls, logs := retryFixture(t, first, alternateText, 200)
			if err != nil || calls != 2 || len(out.Choices) != 1 || out.Choices[0].Message.Content != "fixture complete" {
				t.Fatalf("calls=%d err=%v out=%+v", calls, err, out)
			}
			if strings.Count(logs, "[allmodels] empty completion ") != 1 || !strings.Contains(logs, "[allmodels] retry empty completion") {
				t.Fatal("missing first-attempt diagnostic/retry event")
			}
		})
	}
}

func TestEmptyDoesNotRetryUnsafeOrUnidentifiedResponse(t *testing.T) {
	cases := map[string]string{}
	for _, provider := range []string{`null`, `""`, `"unknown"`, `"UNKNOWN"`, `"none"`, `"undefined"`, `" up-a"`, `"up-a "`, `"up/a"`, `"up.a"`, `"up\na"`, `"up\u0430"`, `"-up"`, `"up:abc"`, `"up-fixture-key"`, `{}`, `123`, `"` + strings.Repeat("a", 97) + `"`} {
		cases["provider_"+provider] = retryWire(provider, `"stop"`, `{"content":""}`)
	}
	for _, reason := range []string{`null`, `""`, `"length"`, `"content_filter"`, `"refusal"`, `"blocked"`, `"safety"`, `"unknown"`, `"tool_calls"`, `"function_call"`, `"STOP"`} {
		cases["reason_"+reason] = retryWire(`"upstream-a"`, reason, `{"content":""}`)
	}
	for name, message := range map[string]string{
		"refusal": `{"refusal":"PRIVATE_REFUSAL"}`, "malformed_refusal": `{"refusal":true}`, "tools": `{"tool_calls":[{"id":"fixture","type":"function","function":{"name":"fixture_marker","arguments":"{}"}}]}`, "empty_tools": `{"tool_calls":[]}`, "function": `{"function_call":{"name":"fixture_marker","arguments":"{}"}}`, "empty_function": `{"function_call":{}}`,
	} {
		cases[name] = retryWire(`"upstream-a"`, `"stop"`, message)
	}
	cases["missing_provider"] = `{"choices":[{"finish_reason":"stop","message":{}}]}`
	cases["zero_choices"] = `{"provider":"upstream-a","choices":[]}`
	cases["missing_message"] = `{"provider":"upstream-a","choices":[{"finish_reason":"stop"}]}`
	cases["multiple_choices"] = `{"provider":"upstream-a","choices":[{"finish_reason":"stop","message":{}},{"finish_reason":"content_filter","message":{}}]}`
	for _, signal := range []string{"refusal", "content_filter", "content_filter_results", "prompt_filter_results", "blocked", "safety", "moderation"} {
		cases["root_"+signal] = strings.TrimSuffix(technicalEmpty, "}") + `,"` + signal + `":{"flag":true}}`
		cases["choice_"+signal] = `{"provider":"upstream-a","choices":[{"finish_reason":"stop","message":{},"` + signal + `":{"flag":true}}]}`
		cases["message_"+signal] = retryWire(`"upstream-a"`, `"stop"`, `{"`+signal+`":{"flag":true}}`)
	}
	for _, signal := range []string{"Refusal", "REFUSAL", "refuſal", "CONTENT_FILTER", "Content_Filter_Results", "PROMPT_FILTER_RESULTS", "Blocked", "Safety", "MODERATION"} {
		cases["variant_root_"+signal] = strings.TrimSuffix(technicalEmpty, "}") + `,"` + signal + `":{"flag":true}}`
		cases["variant_choice_"+signal] = `{"provider":"upstream-a","choices":[{"finish_reason":"stop","message":{},"` + signal + `":{"flag":true}}]}`
		cases["variant_message_"+signal] = retryWire(`"upstream-a"`, `"stop"`, `{"`+signal+`":"PRIVATE_REFUSAL"}`)
	}
	cases["variant_tools"] = retryWire(`"upstream-a"`, `"stop"`, `{"Tool_Calls":[]}`)
	cases["variant_function"] = retryWire(`"upstream-a"`, `"stop"`, `{"Function_Call":{}}`)
	cases["conflicting_content_case"] = retryWire(`"upstream-a"`, `"stop"`, `{"Content":"PRIVATE_NEUTRAL_PROMPT","content":""}`)
	cases["null_message"] = `{"provider":"upstream-a","choices":[{"finish_reason":"stop","message":null}]}`
	cases["missing_reason"] = `{"provider":"upstream-a","choices":[{"message":{}}]}`
	cases["malformed_content"] = retryWire(`"upstream-a"`, `"stop"`, `{"content":[]}`)
	cases["malformed_tools"] = retryWire(`"upstream-a"`, `"stop"`, `{"tool_calls":{}}`)
	cases["malformed_function"] = retryWire(`"upstream-a"`, `"stop"`, `{"function_call":[]}`)
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			out, err, calls, _ := retryFixture(t, first, alternateText, 200)
			if err == nil || calls != 1 || len(out.Choices) != 0 {
				t.Fatalf("unsafe retry/response: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestValidOutputDoesNotRetry(t *testing.T) {
	for _, first := range []string{retryWire(`"upstream-a"`, `"stop"`, `{"content":"fixture complete"}`), retryWire(`"upstream-a"`, `"tool_calls"`, `{"tool_calls":[{"id":"fixture","type":"function","function":{"name":"fixture_marker","arguments":"{}"}}]}`)} {
		_, err, calls, logs := retryFixture(t, first, alternateText, 200)
		if err != nil || calls != 1 || logs != "" {
			t.Fatalf("valid completion: calls=%d err=%v logs=%q", calls, err, logs)
		}
	}
}

func TestRetryStopsAfterSecondAndPreservesEmptySentinel(t *testing.T) {
	for _, tc := range []struct {
		name, second      string
		status, emptyLogs int
	}{
		{"empty", retryWire(`"upstream-b"`, `"stop"`, `{}`), 200, 2},
		{"same_provider_text", retryWire(`"UPSTREAM-A"`, `"stop"`, `{"content":"fixture complete"}`), 200, 1},
		{"same_provider_empty", technicalEmpty, 200, 2},
		{"missing_provider", `{"choices":[{"finish_reason":"stop","message":{"content":"fixture complete"}}]}`, 200, 1},
		{"invalid_provider", retryWire(`"up/invalid"`, `"stop"`, `{"content":"fixture complete"}`), 200, 1},
		{"no_eligible", `{"error":"PRIVATE_ERROR"}`, 404, 1},
		{"http_error", `{"error":"PRIVATE_ERROR"}`, 500, 1},
		{"envelope_error", `{"error":"PRIVATE_ERROR"}`, 200, 1},
		{"bad_json", `not json PRIVATE_ERROR`, 200, 1},
		{"refusal", retryWire(`"upstream-b"`, `"stop"`, `{"refusal":"PRIVATE_REFUSAL"}`), 200, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err, calls, logs := retryFixture(t, technicalEmpty, tc.second, tc.status)
			if !errors.Is(err, ErrEmptyCompletion) || calls != 2 || len(out.Choices) != 0 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if strings.Count(logs, "[allmodels] empty completion ") != tc.emptyLogs {
				t.Fatalf("missing empty diagnostics: %s", logs)
			}
		})
	}
}

func TestNoRetryOnFirstExchangeFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"http", technicalEmpty, 404}, {"envelope", `{"error":"PRIVATE_ERROR"}`, 200}, {"decode", `PRIVATE_ERROR`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := NewClient("fixture-key", WithHTTPClient(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}))
			_, err := c.CreateChatCompletion(context.Background(), retryRequest())
			if err == nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
	for _, attempt := range []int{1, 2} {
		for _, kind := range []string{"transport", "timeout", "cancel"} {
			t.Run(kind+string(rune('0'+attempt)), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				c := NewClient("fixture-key", WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls < attempt {
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(technicalEmpty))}, nil
					}
					if kind == "cancel" {
						cancel()
						return nil, ctx.Err()
					}
					if kind == "timeout" {
						return nil, context.DeadlineExceeded
					}
					return nil, errors.New("PRIVATE_ERROR")
				})}))
				_, err := c.CreateChatCompletion(ctx, retryRequest())
				if err == nil || calls != attempt || strings.Contains(err.Error(), "PRIVATE_ERROR") {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				if kind == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("lost cancellation")
				}
				if attempt == 2 && !errors.Is(err, ErrEmptyCompletion) {
					t.Fatal("lost empty sentinel")
				}
			})
		}
	}
}

func TestCallerDeadlineNotResetAcrossRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	calls := 0
	c := NewClient("fixture-key", WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		got, _ := r.Context().Deadline()
		if !got.Equal(want) {
			t.Fatal("caller deadline changed")
		}
		body := technicalEmpty
		if calls == 2 {
			body = alternateText
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}))
	_, err := c.CreateChatCompletion(ctx, retryRequest())
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestCanceledAfterEmptyDoesNotStartRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	c := NewClient("fixture-key", WithHTTPClient(&http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(technicalEmpty))}, nil
	})}))
	out, err := c.CreateChatCompletion(ctx, retryRequest())
	if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrEmptyCompletion) || len(out.Choices) != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRetryAcceptsUsableProviderIDsWithoutRepair(t *testing.T) {
	for _, id := range []string{"Upstream_123", "123-up", strings.Repeat("a", 96)} {
		first := retryWire(`"`+id+`"`, `"stop"`, `{}`)
		_, err, calls, _ := retryFixture(t, first, alternateText, 200)
		if calls != 2 || err != nil {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}
