package facts

import (
	"fmt"
	"strings"
)

type ChatPrompt struct {
	ReplyStyle    []string
	StableContext []string
	Avoid         []string
}

type ChatDelta struct {
	ReplyStyle    []DeltaOp
	StableContext []DeltaOp
	Avoid         []DeltaOp
}

const (
	maxReplyStyleBullets    = 5
	maxStableContextBullets = 15
	maxAvoidBullets         = 4
)

func ParseChatPrompt(text string) *ChatPrompt {
	prompt := &ChatPrompt{}
	normalized := normalizeWhitespace(text)
	if normalized == "" {
		return prompt
	}

	var section *[]string
	foundHeading := false
	for _, rawLine := range strings.Split(normalized, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch line {
		case "Reply style:", "Communication norms:":
			section = &prompt.ReplyStyle
			foundHeading = true
			continue
		case "Stable context:", "Recurring topics:", "Group dynamics:":
			section = &prompt.StableContext
			foundHeading = true
			continue
		case "Avoid:":
			section = &prompt.Avoid
			foundHeading = true
			continue
		}

		if strings.HasSuffix(line, ":") {
			section = nil
			if !foundHeading {
				continue
			}
			continue
		}

		if !foundHeading {
			prompt.StableContext = append(prompt.StableContext, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
			continue
		}

		if section == nil {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			item := strings.TrimSpace(line[2:])
			if item != "" {
				*section = append(*section, item)
			}
		}
	}

	if !foundHeading && len(prompt.StableContext) == 0 {
		prompt.StableContext = append(prompt.StableContext, normalized)
	}

	return prompt
}

func RenderChatPrompt(prompt *ChatPrompt) string {
	if prompt == nil {
		prompt = &ChatPrompt{}
	}

	var out strings.Builder
	writeSection(&out, "Reply style:", prompt.ReplyStyle)
	out.WriteString("\n\n")
	writeSection(&out, "Stable context:", prompt.StableContext)
	out.WriteString("\n\n")
	writeSection(&out, "Avoid:", prompt.Avoid)
	return strings.TrimSpace(out.String())
}

func EvaluateChatDelta(raw string) (*ChatDelta, bool, bool, string) {
	value := normalizeWhitespace(raw)
	if value == "" {
		return nil, false, true, "empty output"
	}
	if looksLikeMetaFactsOutput(value) {
		return nil, false, true, "meta output"
	}
	delta, err := ParseChatDelta(value)
	if err != nil {
		return nil, false, true, err.Error()
	}
	if totalDeltaOps(delta.ReplyStyle, delta.StableContext, delta.Avoid) == 0 {
		return nil, false, true, "no delta ops"
	}
	return delta, true, false, ""
}

func ParseChatDelta(text string) (*ChatDelta, error) {
	delta := &ChatDelta{}
	var section *[]DeltaOp

	for _, rawLine := range strings.Split(normalizeWhitespace(text), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch line {
		case "Reply style:", "Communication norms:":
			section = &delta.ReplyStyle
			continue
		case "Stable context:", "Recurring topics:", "Group dynamics:":
			section = &delta.StableContext
			continue
		case "Avoid:":
			section = &delta.Avoid
			continue
		}

		if section == nil {
			return nil, fmt.Errorf("delta line outside section")
		}

		op, err := parseDeltaOp(line)
		if err != nil {
			return nil, err
		}
		*section = append(*section, op)
	}

	return delta, nil
}

func ApplyChatDelta(current *ChatPrompt, delta *ChatDelta) *ChatPrompt {
	if current == nil {
		current = &ChatPrompt{}
	}
	if delta == nil {
		return cloneChatPrompt(current)
	}

	merged := cloneChatPrompt(current)
	merged.ReplyStyle = applyDeltaSection(merged.ReplyStyle, delta.ReplyStyle)
	merged.StableContext = applyDeltaSection(merged.StableContext, delta.StableContext)
	merged.Avoid = applyDeltaSection(merged.Avoid, delta.Avoid)
	return merged
}

func SanitizeChatDelta(delta *ChatDelta) *ChatDelta {
	if delta == nil {
		return nil
	}
	return &ChatDelta{
		ReplyStyle:    sanitizeChatDeltaSection(delta.ReplyStyle),
		StableContext: sanitizeChatDeltaSection(delta.StableContext),
		Avoid:         sanitizeChatDeltaSection(delta.Avoid),
	}
}

func FilterChatDelta(current *ChatPrompt, delta *ChatDelta) *ChatDelta {
	if delta == nil {
		return nil
	}
	if current == nil {
		current = &ChatPrompt{}
	}
	return &ChatDelta{
		ReplyStyle:    filterChatDeltaSection(current.ReplyStyle, delta.ReplyStyle),
		StableContext: filterChatDeltaSection(current.StableContext, delta.StableContext),
		Avoid:         filterChatDeltaSection(current.Avoid, delta.Avoid),
	}
}

func EnforceChatPromptBudgets(prompt *ChatPrompt) *ChatPrompt {
	if prompt == nil {
		return &ChatPrompt{}
	}
	return &ChatPrompt{
		ReplyStyle:    limitChatPromptSection(prompt.ReplyStyle, maxReplyStyleBullets),
		StableContext: limitChatPromptSection(prompt.StableContext, maxStableContextBullets),
		Avoid:         limitChatPromptSection(prompt.Avoid, maxAvoidBullets),
	}
}

func cloneChatPrompt(current *ChatPrompt) *ChatPrompt {
	return &ChatPrompt{
		ReplyStyle:    append([]string(nil), current.ReplyStyle...),
		StableContext: append([]string(nil), current.StableContext...),
		Avoid:         append([]string(nil), current.Avoid...),
	}
}

func sanitizeChatDeltaSection(ops []DeltaOp) []DeltaOp {
	if len(ops) == 0 {
		return nil
	}
	cleaned := make([]DeltaOp, 0, len(ops))
	for _, op := range ops {
		op.NewText = sanitizeChatPromptBullet(op.NewText)
		if op.NewText == "" {
			continue
		}
		if op.Action == '+' {
			op.Text = op.NewText
		}
		if op.Action == '~' {
			op.OldText = sanitizeChatPromptBullet(op.OldText)
			if op.OldText == "" {
				op.Action = '+'
				op.Text = op.NewText
			} else {
				op.Text = op.OldText + " -> " + op.NewText
			}
		}
		cleaned = append(cleaned, op)
	}
	return cleaned
}

func sanitizeChatPromptBullet(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.TrimPrefix(text, "new guidance:")
	text = strings.TrimSpace(text)
	if strings.Contains(text, "->") {
		if parts := strings.SplitN(text, "->", 2); len(parts) == 2 {
			text = strings.TrimSpace(parts[1])
		}
	}
	text = strings.TrimPrefix(text, "- ")
	text = strings.TrimSpace(text)
	return text
}

func filterChatDeltaSection(current []string, ops []DeltaOp) []DeltaOp {
	filtered := make([]DeltaOp, 0, len(ops))
	projected := append([]string(nil), current...)
	for _, op := range ops {
		candidate := strings.TrimSpace(op.NewText)
		if candidate == "" || isWeakChatPromptBullet(candidate) {
			continue
		}
		switch op.Action {
		case '+':
			if isWeakerThanExisting(projected, candidate) {
				continue
			}
			filtered = append(filtered, op)
			projected = appendUniqueString(projected, candidate)
		case '~':
			idx := findMatchingItem(projected, op.OldText)
			if idx >= 0 {
				if !isBetterThanExisting(projected[idx], candidate) {
					continue
				}
				filtered = append(filtered, op)
				projected[idx] = candidate
				continue
			}
			if isWeakerThanExisting(projected, candidate) {
				continue
			}
			filtered = append(filtered, DeltaOp{Action: '+', Text: candidate, NewText: candidate})
			projected = appendUniqueString(projected, candidate)
		}
	}
	return filtered
}

func isWeakChatPromptBullet(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "new guidance:") || strings.Contains(lower, "->") {
		return true
	}
	weak := []string{
		"the chat discusses topics",
		"users talk casually",
		"the group chats",
		"people send messages",
		"there is a bot",
	}
	for _, marker := range weak {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func limitChatPromptSection(items []string, limit int) []string {
	if len(items) <= limit {
		return append([]string(nil), items...)
	}
	return append([]string(nil), items[:limit]...)
}
