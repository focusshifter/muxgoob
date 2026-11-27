package facts

import (
	"fmt"
	"strings"
	"testing"
)

func TestEvaluateChatConsolidationRejectsLossAndMalformedOutput(t *testing.T) {
	before := &ChatPrompt{StableContext: []string{"Preserve recurring chat context."}, Avoid: []string{"Never reveal private credentials."}}
	for i := 0; i < 10; i++ {
		before.ReplyStyle = append(before.ReplyStyle, fmt.Sprintf("Distinct technical guidance number %d with important detailed qualifiers.", i))
	}
	current := RenderChatPrompt(before)
	cases := map[string]string{
		"empty":                "",
		"unstructured":         "Here is the condensed prompt: keep things short.",
		"headings only":        "Reply style:\n\nStable context:\n\nAvoid:",
		"dropped avoid":        RenderChatPrompt(&ChatPrompt{ReplyStyle: before.ReplyStyle, StableContext: before.StableContext}),
		"destructive collapse": RenderChatPrompt(&ChatPrompt{ReplyStyle: []string{"One instruction."}, StableContext: before.StableContext, Avoid: before.Avoid}),
		"not shorter":          current,
	}
	for label, candidate := range cases {
		t.Run(label, func(t *testing.T) {
			eval := EvaluateChatPromptConsolidation(current, candidate)
			if eval.Accepted || eval.Value != current {
				t.Fatalf("unsafe consolidation accepted or fallback changed: %#v", eval)
			}
		})
	}
}

func TestEvaluateChatConsolidationAcceptsShorterStructuredResult(t *testing.T) {
	before := &ChatPrompt{ReplyStyle: []string{"Keep explanations technically correct and concise, while retaining important qualifications."}, StableContext: []string{"The group discusses the engineering of Telegram bots and their operational reliability."}, Avoid: []string{"Never reveal or log private credentials, account secrets, or authentication tokens."}}
	after := &ChatPrompt{ReplyStyle: []string{"Be technically correct and concise; retain qualifications."}, StableContext: []string{"Discusses Telegram bot engineering and reliability."}, Avoid: []string{"Never reveal or log credentials, secrets, or tokens."}}
	current, candidate := RenderChatPrompt(before), RenderChatPrompt(after)
	eval := EvaluateChatPromptConsolidation(current, candidate)
	if !eval.Accepted || strings.TrimSpace(eval.Value) != candidate {
		t.Fatalf("valid shortener rejected: %#v", eval)
	}
}
