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

	openai "github.com/sashabaranov/go-openai"
)

const validStopCall = `{"id":"call-fixture","type":"function","function":{"name":"fixture_marker","arguments":"{\"PRIVATE_ARGUMENT\":1}"}}`

func normalizationRequest() openai.ChatCompletionRequest {
	req := retryRequest()
	req.ToolChoice = "auto"
	return req
}

func normalizeFixture(t *testing.T, req openai.ChatCompletionRequest, responses ...string) (openai.ChatCompletionResponse, error, int, string) {
	t.Helper()
	var logs bytes.Buffer
	oldWriter, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() { log.SetOutput(oldWriter); log.SetFlags(oldFlags) }()
	calls := 0
	before, _ := json.Marshal(req)
	c := NewClient("fixture-key", WithHTTPClient(&http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > len(responses) {
			t.Fatal("unexpected extra POST")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		provider := body["provider"].(map[string]any)
		if provider["zdr"] != true || body["model"] != "fixture/model" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Fatal("privacy/model/auth changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(responses[calls-1]))}, nil
	})}))
	out, err := c.CreateChatCompletion(context.Background(), req)
	after, _ := json.Marshal(req)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated request")
	}
	for _, secret := range []string{"fixture-key", "PRIVATE_ARGUMENT", "PRIVATE_REASONING", "fixture_marker"} {
		if strings.Contains(logs.String(), secret) || err != nil && strings.Contains(err.Error(), secret) {
			t.Fatalf("private data logged: %s", secret)
		}
	}
	return out, err, calls, logs.String()
}

func TestStopToolsNormalizeFirstAndRetry(t *testing.T) {
	for _, choice := range []any{nil, "auto", "required", openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "fixture_marker"}}, &openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "fixture_marker"}}} {
		for _, retry := range []bool{false, true} {
			req := normalizationRequest()
			req.ToolChoice = choice
			wire := retryWire(`"upstream-b"`, `"stop"`, `{"content":"preamble","reasoning_content":"PRIVATE_REASONING","tool_calls":[`+validStopCall+`]}`)
			responses := []string{wire}
			if retry {
				responses = append([]string{technicalEmpty}, responses...)
			}
			out, err, calls, logs := normalizeFixture(t, req, responses...)
			if err != nil || calls != len(responses) || len(out.Choices) != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			got := out.Choices[0]
			if got.FinishReason != openai.FinishReasonToolCalls || got.Message.Content != "preamble" || len(got.Message.ToolCalls) != 1 || got.Message.ToolCalls[0].ID != "call-fixture" || got.Message.ToolCalls[0].Function.Arguments != `{"PRIVATE_ARGUMENT":1}` {
				t.Fatalf("lost tool continuation: %+v", got)
			}
			if !strings.Contains(logs, "normalized tool calls") {
				t.Fatal("missing bounded normalization diagnostic")
			}
			continuation := openai.ChatCompletionMessage{Role: "tool", ToolCallID: got.Message.ToolCalls[0].ID, Content: "fixture result"}
			req.Messages = append(req.Messages, got.Message, continuation)
			final, err, _, _ := normalizeFixture(t, req, alternateText)
			if err != nil || final.Choices[0].Message.Content != "fixture complete" {
				t.Fatal("continuation invalid", err)
			}
		}
	}
}

func TestStopToolsRejectInvalidEvenWithText(t *testing.T) {
	base := retryWire(`"upstream-a"`, `"stop"`, `{"content":"preamble","tool_calls":[`+validStopCall+`]}`)
	cases := map[string]string{
		"blank_id":          strings.Replace(base, `call-fixture`, ` `, 1),
		"blank_name":        strings.Replace(base, `fixture_marker`, ` `, 1),
		"undeclared":        strings.Replace(base, `fixture_marker`, `other`, 1),
		"wrong_type":        strings.Replace(base, `"type":"function"`, `"type":"other"`, 1),
		"duplicate_ids":     strings.Replace(base, validStopCall, validStopCall+`,`+validStopCall, 1),
		"empty_tools":       strings.Replace(base, validStopCall, ``, 1),
		"legacy":            strings.Replace(base, `"tool_calls":`, `"function_call":{},"tool_calls":`, 1),
		"duplicate_key":     strings.Replace(base, `"id":"call-fixture"`, `"id":"bad","id":"call-fixture"`, 1),
		"case_collision":    strings.Replace(base, `"id":"call-fixture"`, `"ID":"bad","id":"call-fixture"`, 1),
		"unicode_collision": strings.Replace(base, `"arguments":`, `"argumentſ":"{}","arguments":`, 1),
		"reason_collision":  strings.Replace(base, `"finish_reason":"stop"`, `"Finish_Reason":"content_filter","finish_reason":"stop"`, 1),
		"message_collision": strings.Replace(base, `"message":`, `"Message":{},"message":`, 1),
		"tools_collision":   strings.Replace(base, `"tool_calls":`, `"Tool_Calls":null,"tool_calls":`, 1),
		"multiple_choices":  strings.Replace(base, `"choices":[`, `"choices":[{"finish_reason":"stop","message":{}},`, 1),
	}
	for _, args := range []string{`null`, `[]`, `1`, `"scalar"`, `invalid`, `{"x":1,"x":2}`} {
		encoded, _ := json.Marshal(args)
		cases["args_"+args] = strings.Replace(base, `"{\"PRIVATE_ARGUMENT\":1}"`, string(encoded), 1)
	}
	for _, signal := range []string{"refusal", "Refuſal", "content_filter", "Content_Filter_Results", "prompt_filter_results", "blocked", "safety", "moderation", "finish_reason"} {
		if signal != "finish_reason" {
			cases["root_"+signal] = strings.TrimSuffix(base, "}") + `,"` + signal + `":"PRIVATE_REFUSAL"}`
		}
		cases["message_"+signal] = strings.Replace(base, `"content":`, `"`+signal+`":"PRIVATE_REFUSAL","content":`, 1)
		cases["choice_"+signal] = strings.Replace(base, `"message":`, `"`+signal+`":"PRIVATE_REFUSAL","message":`, 1)
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			out, err, calls, _ := normalizeFixture(t, normalizationRequest(), wire)
			if !errors.Is(err, ErrEmptyCompletion) || calls != 1 || len(out.Choices) != 0 {
				t.Fatalf("unsafe acceptance: calls=%d err=%v out=%+v", calls, err, out)
			}
		})
	}
	for _, mutate := range []func(*openai.ChatCompletionRequest){
		func(r *openai.ChatCompletionRequest) { r.Tools = nil },
		func(r *openai.ChatCompletionRequest) { r.ToolChoice = "none" },
		func(r *openai.ChatCompletionRequest) { r.ToolChoice = "unknown" },
		func(r *openai.ChatCompletionRequest) {
			r.ToolChoice = openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "other"}}
		},
		func(r *openai.ChatCompletionRequest) {
			r.Tools = append(r.Tools, openai.Tool{Type: openai.ToolTypeFunction, Function: &openai.FunctionDefinition{Name: "other"}})
			r.ToolChoice = openai.ToolChoice{Type: openai.ToolTypeFunction, Function: openai.ToolFunction{Name: "other"}}
		},
		func(r *openai.ChatCompletionRequest) { r.Tools[0].Function = nil },
		func(r *openai.ChatCompletionRequest) {
			r.ToolChoice = map[string]any{"type": "other", "function": map[string]any{"name": "fixture_marker"}}
		},
	} {
		req := normalizationRequest()
		mutate(&req)
		_, err, _, _ := normalizeFixture(t, req, base)
		if !errors.Is(err, ErrEmptyCompletion) {
			t.Fatalf("disallowed request: %v", err)
		}
	}
}

func TestStopToolsNullContentAndParallelCalls(t *testing.T) {
	for _, content := range []string{`null`, `""`, `"preamble"`} {
		second := strings.Replace(validStopCall, `call-fixture`, `call-second`, 1)
		wire := retryWire(`"upstream-a"`, `"stop"`, `{"content":`+content+`,"refusal":null,"function_call":null,"tool_calls":[`+validStopCall+`,`+second+`]}`)
		var original openai.ChatCompletionResponse
		if err := json.Unmarshal([]byte(wire), &original); err != nil {
			t.Fatal(err)
		}
		out, err, calls, _ := normalizeFixture(t, normalizationRequest(), wire)
		if err != nil || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
		original.Choices[0].FinishReason = openai.FinishReasonToolCalls
		if !reflect.DeepEqual(original, out) {
			t.Fatal("normalization changed fields beyond finish reason")
		}
	}
}

func TestAmbiguousRequestRejectedBeforePOST(t *testing.T) {
	for _, choice := range []any{
		map[string]any{"type": "function", "Type": "other", "function": map[string]any{"name": "fixture_marker"}},
		json.RawMessage(`{"type":"other","type":"function","function":{"name":"fixture_marker"}}`),
		json.RawMessage(`{"type":"function","function":{"name":"other","name":"fixture_marker"}}`),
	} {
		req := normalizationRequest()
		req.ToolChoice = choice
		_, err, calls, _ := normalizeFixture(t, req)
		if err == nil || calls != 0 {
			t.Fatalf("ambiguous request sent: calls=%d err=%v", calls, err)
		}
	}
}

func TestRetryInvalidStopToolsRemainUnusable(t *testing.T) {
	for _, provider := range []string{`"upstream-b"`, `"UPSTREAM-A"`, `null`} {
		for _, message := range []string{
			`{"content":"preamble","tool_calls":[` + strings.Replace(validStopCall, `fixture_marker`, `undeclared`, 1) + `]}`,
			`{"content":"preamble","refusal":"PRIVATE_REFUSAL","tool_calls":[` + validStopCall + `]}`,
		} {
			out, err, calls, _ := normalizeFixture(t, normalizationRequest(), technicalEmpty, retryWire(provider, `"stop"`, message))
			if !errors.Is(err, ErrEmptyCompletion) || calls != 2 || len(out.Choices) != 0 {
				t.Fatalf("unsafe alternate accepted: calls=%d err=%v", calls, err)
			}
		}
	}
}

func TestOtherFinishReasonsNotNormalized(t *testing.T) {
	for _, reason := range []string{"length", "content_filter", "unknown", "tool_calls"} {
		wire := retryWire(`"upstream-a"`, `"`+reason+`"`, `{"tool_calls":[`+validStopCall+`]}`)
		out, err, _, logs := normalizeFixture(t, normalizationRequest(), wire)
		if strings.Contains(logs, "normalized tool calls") {
			t.Fatal("changed non-stop")
		}
		if reason == "tool_calls" {
			if err != nil || !reflect.DeepEqual(out.Choices[0].FinishReason, openai.FinishReasonToolCalls) {
				t.Fatal("broke ordinary continuation")
			}
		} else if err == nil {
			t.Fatal("accepted incomplete tool response")
		}
	}
}
