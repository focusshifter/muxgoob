package facts

import "testing"

func TestParseChatPromptStructured(t *testing.T) {
	input := "Reply style:\n- answer in Russian with dry sarcasm\n\nStable context:\n- Slay the Spire 2 sessions are a recurring topic\n\nAvoid:\n- do not answer like a formal assistant"
	parsed := ParseChatPrompt(input)
	if len(parsed.ReplyStyle) != 1 || len(parsed.StableContext) != 1 || len(parsed.Avoid) != 1 {
		t.Fatalf("unexpected parsed prompt: %#v", parsed)
	}
}

func TestParseChatPromptLegacyFreeform(t *testing.T) {
	parsed := ParseChatPrompt("Mostly Russian chat about games and memes")
	if len(parsed.StableContext) != 1 || parsed.StableContext[0] != "Mostly Russian chat about games and memes" {
		t.Fatalf("unexpected legacy parse: %#v", parsed)
	}
}

func TestApplyChatDelta(t *testing.T) {
	current := &ChatPrompt{StableContext: []string{"games"}}
	delta := &ChatDelta{ReplyStyle: []DeltaOp{{Action: '+', NewText: "answer in Russian with dry sarcasm"}}}
	merged := ApplyChatDelta(current, delta)
	if len(merged.ReplyStyle) != 1 || merged.ReplyStyle[0] != "answer in Russian with dry sarcasm" {
		t.Fatalf("unexpected merged prompt: %#v", merged)
	}
}

func TestParseChatPromptLegacyStructuredHeadings(t *testing.T) {
	input := "Recurring topics:\n- Wordle\n\nGroup dynamics:\n- friendly teasing\n\nCommunication norms:\n- mostly Russian"
	parsed := ParseChatPrompt(input)
	if len(parsed.StableContext) != 2 || len(parsed.ReplyStyle) != 1 {
		t.Fatalf("unexpected legacy structured parse: %#v", parsed)
	}
}

func TestSanitizeChatDeltaRemovesArrowLeakage(t *testing.T) {
	delta := &ChatDelta{
		Avoid: []DeltaOp{{Action: '+', NewText: "new guidance: old thing -> Better replacement"}},
	}
	cleaned := SanitizeChatDelta(delta)
	if cleaned.Avoid[0].NewText != "Better replacement" {
		t.Fatalf("unexpected sanitized chat delta: %#v", cleaned.Avoid[0])
	}
}

func TestEnforceChatPromptBudgets(t *testing.T) {
	prompt := &ChatPrompt{
		ReplyStyle:    []string{"1", "2", "3", "4", "5", "6"},
		StableContext: []string{"1", "2", "3", "4", "5", "6", "7"},
		Avoid:         []string{"1", "2", "3", "4", "5"},
	}
	limited := EnforceChatPromptBudgets(prompt)
	if len(limited.ReplyStyle) != 5 || len(limited.StableContext) != 6 || len(limited.Avoid) != 4 {
		t.Fatalf("unexpected prompt budgets: %#v", limited)
	}
}
