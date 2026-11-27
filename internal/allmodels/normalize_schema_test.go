package allmodels

import (
	"strings"
	"testing"
)

func TestNormalizationPreservesCaseSensitiveSchemaAndArguments(t *testing.T) {
	for _, reason := range []string{"stop", "tool_calls", "text"} {
		t.Run(reason, func(t *testing.T) {
			req := normalizationRequest()
			req.Tools[0].Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "integer"}, "X": map[string]any{"type": "integer"}}}
			call := strings.Replace(validStopCall, `"arguments":"{\"PRIVATE_ARGUMENT\":1}"`, `"arguments":"{\"x\":1,\"X\":2}"`, 1)
			wire := retryWire(`"upstream-a"`, `"`+reason+`"`, `{"tool_calls":[`+call+`]}`)
			if reason == "text" {
				wire = retryWire(`"upstream-a"`, `"stop"`, `{"content":"fixture text"}`)
			}
			out, err, calls, _ := normalizeFixture(t, req, wire)
			if err != nil || calls != 1 || len(out.Choices) != 1 {
				t.Fatalf("valid case-sensitive schema rejected: calls=%d err=%v", calls, err)
			}
			if reason != "text" && out.Choices[0].Message.ToolCalls[0].Function.Arguments != `{"x":1,"X":2}` {
				t.Fatal("case-sensitive argument keys changed")
			}
		})
	}
}
