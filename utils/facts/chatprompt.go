package facts

import (
	"fmt"
	"strings"
)

type ChatPrompt struct {
	Topics   []string
	Dynamics []string
	Norms    []string
}

type ChatDelta struct {
	Topics   []DeltaOp
	Dynamics []DeltaOp
	Norms    []DeltaOp
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
		case "Recurring topics:":
			section = &prompt.Topics
			foundHeading = true
			continue
		case "Group dynamics:":
			section = &prompt.Dynamics
			foundHeading = true
			continue
		case "Communication norms:":
			section = &prompt.Norms
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
			prompt.Topics = append(prompt.Topics, strings.TrimSpace(strings.TrimPrefix(line, "- ")))
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

	if !foundHeading && len(prompt.Topics) == 0 {
		prompt.Topics = append(prompt.Topics, normalized)
	}

	return prompt
}

func RenderChatPrompt(prompt *ChatPrompt) string {
	if prompt == nil {
		prompt = &ChatPrompt{}
	}

	var out strings.Builder
	writeSection(&out, "Recurring topics:", prompt.Topics)
	out.WriteString("\n\n")
	writeSection(&out, "Group dynamics:", prompt.Dynamics)
	out.WriteString("\n\n")
	writeSection(&out, "Communication norms:", prompt.Norms)
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
	if totalDeltaOps(delta.Topics, delta.Dynamics, delta.Norms) == 0 {
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
		case "Recurring topics:":
			section = &delta.Topics
			continue
		case "Group dynamics:":
			section = &delta.Dynamics
			continue
		case "Communication norms:":
			section = &delta.Norms
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
	merged.Topics = applyDeltaSection(merged.Topics, delta.Topics)
	merged.Dynamics = applyDeltaSection(merged.Dynamics, delta.Dynamics)
	merged.Norms = applyDeltaSection(merged.Norms, delta.Norms)
	return merged
}

func cloneChatPrompt(current *ChatPrompt) *ChatPrompt {
	return &ChatPrompt{
		Topics:   append([]string(nil), current.Topics...),
		Dynamics: append([]string(nil), current.Dynamics...),
		Norms:    append([]string(nil), current.Norms...),
	}
}
