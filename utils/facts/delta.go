package facts

import (
	"fmt"
	"strings"
)

type DeltaOp struct {
	Action  byte
	Text    string
	OldText string
	NewText string
}

type Delta struct {
	Identity      []DeltaOp
	Interests     []DeltaOp
	Relationships []DeltaOp
}

func IsNoChanges(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "NO_CHANGES")
}

func EvaluateDelta(raw string) (*Delta, bool, bool, string) {
	value := normalizeWhitespace(raw)
	if value == "" {
		return nil, false, true, "empty output"
	}
	if looksLikeMetaFactsOutput(value) {
		return nil, false, true, "meta output"
	}
	delta, err := ParseDelta(value)
	if err != nil {
		return nil, false, true, err.Error()
	}
	if totalDeltaOps(delta.Identity, delta.Interests, delta.Relationships) == 0 {
		return nil, false, true, "no delta ops"
	}
	return delta, true, false, ""
}

func ParseDelta(text string) (*Delta, error) {
	delta := &Delta{}
	var section *[]DeltaOp

	for _, rawLine := range strings.Split(normalizeWhitespace(text), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch line {
		case "Identity:":
			section = &delta.Identity
			continue
		case "Interests:":
			section = &delta.Interests
			continue
		case "Relationships:":
			section = &delta.Relationships
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

func ApplyDelta(current *Dossier, delta *Delta) *Dossier {
	if current == nil {
		current = &Dossier{}
	}
	if delta == nil {
		return cloneDossier(current)
	}

	merged := cloneDossier(current)
	merged.Identity = applyDeltaSection(merged.Identity, delta.Identity)
	merged.Interests = applyDeltaSection(merged.Interests, delta.Interests)
	merged.Relationships = applyDeltaSection(merged.Relationships, delta.Relationships)
	return merged
}

func cloneDossier(current *Dossier) *Dossier {
	return &Dossier{
		Identity:      append([]string(nil), current.Identity...),
		Interests:     append([]string(nil), current.Interests...),
		Relationships: append([]string(nil), current.Relationships...),
	}
}

func parseDeltaOp(line string) (DeltaOp, error) {
	if strings.HasPrefix(line, "+ ") {
		text := strings.TrimSpace(line[2:])
		if text == "" {
			return DeltaOp{}, fmt.Errorf("empty add delta")
		}
		return DeltaOp{Action: '+', Text: text, NewText: text}, nil
	}

	if strings.HasPrefix(line, "~ ") {
		text := strings.TrimSpace(line[2:])
		parts := strings.SplitN(text, " -> ", 2)
		if len(parts) != 2 {
			return DeltaOp{}, fmt.Errorf("invalid update delta")
		}
		oldText := strings.TrimSpace(parts[0])
		newText := strings.TrimSpace(parts[1])
		if oldText == "" || newText == "" {
			return DeltaOp{}, fmt.Errorf("invalid update delta")
		}
		return DeltaOp{Action: '~', Text: text, OldText: oldText, NewText: newText}, nil
	}

	return DeltaOp{}, fmt.Errorf("invalid delta line")
}

func totalDeltaOps(sections ...[]DeltaOp) int {
	total := 0
	for _, section := range sections {
		total += len(section)
	}
	return total
}

func applyDeltaSection(current []string, ops []DeltaOp) []string {
	merged := append([]string(nil), current...)
	for _, op := range ops {
		switch op.Action {
		case '+':
			merged = appendUniqueString(merged, op.NewText)
		case '~':
			idx := findMatchingItem(merged, op.OldText)
			if idx >= 0 {
				if strings.EqualFold(normalizeListItem(merged[idx]), normalizeListItem(op.NewText)) {
					continue
				}
				merged[idx] = strings.TrimSpace(op.NewText)
				continue
			}
			merged = appendUniqueString(merged, op.NewText)
		}
	}
	return merged
}

func appendUniqueString(items []string, item string) []string {
	item = strings.TrimSpace(item)
	if item == "" {
		return items
	}
	needle := normalizeListItem(item)
	for _, existing := range items {
		if normalizeListItem(existing) == needle {
			return items
		}
	}
	return append(items, item)
}

func findMatchingItem(items []string, query string) int {
	needle := normalizeListItem(query)
	if needle == "" {
		return -1
	}
	for i, item := range items {
		normalized := normalizeListItem(item)
		if normalized == needle || strings.Contains(normalized, needle) || strings.Contains(needle, normalized) {
			return i
		}
	}
	return -1
}

func normalizeListItem(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
