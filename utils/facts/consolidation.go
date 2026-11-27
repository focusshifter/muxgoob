package facts

import "strings"

// EvaluateChatPromptConsolidation checks a rare model-generated shortener result.
// These structural guards reject obvious data loss; they do not prove semantic
// equivalence, which is why normal updates do not invoke a shortener.
func EvaluateChatPromptConsolidation(current, candidate string) Evaluation {
	keep := func(retryable bool, reason string) Evaluation {
		return Evaluation{Value: current, Retryable: retryable, Reason: reason}
	}
	candidate = strings.TrimSpace(candidate)
	if !validConsolidatedChatPrompt(candidate) {
		return keep(true, "invalid chat prompt structure")
	}
	before := ParseChatPrompt(current)
	after := NormalizeChatPrompt(ParseChatPrompt(candidate))
	sectionsBefore := [][]string{before.ReplyStyle, before.StableContext, before.Avoid}
	sectionsAfter := [][]string{after.ReplyStyle, after.StableContext, after.Avoid}
	previousBullets, nextBullets := 0, 0
	for i, section := range sectionsBefore {
		if len(section) > 0 && len(sectionsAfter[i]) == 0 {
			return keep(true, "removed populated chat prompt section")
		}
		previousBullets += len(section)
		nextBullets += len(sectionsAfter[i])
	}
	if previousBullets >= 4 && nextBullets*2 < previousBullets {
		return keep(true, "destructive chat prompt rewrite")
	}
	value := RenderChatPrompt(after)
	if len(value) >= len(strings.TrimSpace(current)) {
		return keep(false, "chat prompt did not get shorter")
	}
	return Evaluation{Value: value, Accepted: true}
}

func validConsolidatedChatPrompt(text string) bool {
	headings := []string{"Reply style:", "Stable context:", "Avoid:"}
	next, bullets := 0, 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if next < len(headings) && line == headings[next] {
			next++
			continue
		}
		if next == 0 || !strings.HasPrefix(line, "- ") || strings.TrimSpace(strings.TrimPrefix(line, "- ")) == "" || strings.Contains(line, "->") {
			return false
		}
		bullets++
	}
	return next == len(headings) && bullets > 0
}
