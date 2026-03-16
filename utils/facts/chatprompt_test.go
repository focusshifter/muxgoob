package facts

import "testing"

func TestParseChatPromptStructured(t *testing.T) {
	input := "Recurring topics:\n- games\n\nGroup dynamics:\n- friendly teasing\n\nCommunication norms:\n- mostly Russian"
	parsed := ParseChatPrompt(input)
	if len(parsed.Topics) != 1 || len(parsed.Dynamics) != 1 || len(parsed.Norms) != 1 {
		t.Fatalf("unexpected parsed prompt: %#v", parsed)
	}
}

func TestParseChatPromptLegacyFreeform(t *testing.T) {
	parsed := ParseChatPrompt("Mostly Russian chat about games and memes")
	if len(parsed.Topics) != 1 || parsed.Topics[0] != "Mostly Russian chat about games and memes" {
		t.Fatalf("unexpected legacy parse: %#v", parsed)
	}
}

func TestApplyChatDelta(t *testing.T) {
	current := &ChatPrompt{Topics: []string{"games"}}
	delta := &ChatDelta{Dynamics: []DeltaOp{{Action: '+', NewText: "friendly teasing"}}}
	merged := ApplyChatDelta(current, delta)
	if len(merged.Dynamics) != 1 || merged.Dynamics[0] != "friendly teasing" {
		t.Fatalf("unexpected merged prompt: %#v", merged)
	}
}
