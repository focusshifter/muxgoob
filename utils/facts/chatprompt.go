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

func cloneChatPrompt(current *ChatPrompt) *ChatPrompt {
	return &ChatPrompt{
		ReplyStyle:    append([]string(nil), current.ReplyStyle...),
		StableContext: append([]string(nil), current.StableContext...),
		Avoid:         append([]string(nil), current.Avoid...),
	}
}
