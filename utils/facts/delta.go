package facts

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	sectionIdentity = iota
	sectionInterests
)

type DeltaOp struct {
	Action  byte
	Text    string
	OldText string
	NewText string
}

type Delta struct {
	Identity  []DeltaOp
	Interests []DeltaOp
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
	if totalDeltaOps(delta.Identity, delta.Interests) == 0 {
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
	return merged
}

func FilterDeltaForDossier(current *Dossier, delta *Delta) *Delta {
	if delta == nil {
		return nil
	}
	if current == nil {
		current = &Dossier{}
	}

	return &Delta{
		Identity:  filterDeltaSection(current.Identity, delta.Identity, sectionIdentity),
		Interests: filterDeltaSection(current.Interests, delta.Interests, sectionInterests),
	}
}

func SanitizeDeltaForPerson(delta *Delta, userName string) *Delta {
	if delta == nil {
		return nil
	}
	return &Delta{
		Identity:  sanitizeDeltaSection(delta.Identity, userName),
		Interests: sanitizeDeltaSection(delta.Interests, userName),
	}
}

func cloneDossier(current *Dossier) *Dossier {
	return &Dossier{
		Identity:  append([]string(nil), current.Identity...),
		Interests: append([]string(nil), current.Interests...),
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

func filterDeltaSection(current []string, ops []DeltaOp, section int) []DeltaOp {
	filtered := make([]DeltaOp, 0, len(ops))
	projected := append([]string(nil), current...)
	for _, op := range ops {
		candidate := strings.TrimSpace(op.NewText)
		if candidate == "" {
			continue
		}
		if isWeakBullet(section, candidate) {
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
			if idx < 0 {
				if isWeakerThanExisting(projected, candidate) {
					continue
				}
				filtered = append(filtered, DeltaOp{Action: '+', Text: candidate, NewText: candidate})
				projected = appendUniqueString(projected, candidate)
				continue
			}
			if !isBetterThanExisting(projected[idx], candidate) {
				continue
			}
			filtered = append(filtered, op)
			projected[idx] = candidate
		}
	}
	return filtered
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

func isWeakBullet(section int, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	if looksLikeStyleInference(text) {
		return true
	}
	if len(strings.Fields(text)) < 3 && !containsConcreteSignal(text) {
		return true
	}

	switch section {
	case sectionIdentity:
		return isWeakIdentityBullet(text)
	case sectionInterests:
		return isWeakInterestBullet(text)
	default:
		return false
	}
}

func isWeakIdentityBullet(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	weakPrefixes := []string{
		"seems to ",
		"appears to ",
		"likely ",
		"probably ",
		"uses humor",
		"has a light-hearted approach",
		"has a playful",
		"is light-hearted",
	}
	for _, prefix := range weakPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func isWeakInterestBullet(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	weakPrefixes := []string{
		"is interested in various",
		"has an interest in",
		"enjoys various",
		"likes games",
		"plays games",
		"interested in games",
		"interested in anime",
		"likes anime",
		"likes music",
	}
	for _, prefix := range weakPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	genericTerms := []string{"various genres", "various games", "different games"}
	for _, term := range genericTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	if !containsConcreteSignal(text) && containsAny(lower, []string{"games", "anime", "music", "movies", "streams"}) {
		return true
	}
	return false
}

func looksLikeStyleInference(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	markers := []string{
		"humor",
		"humorous",
		"sarcast",
		"playful",
		"light-hearted",
		"banter",
		"approach to conversations",
		"tone",
		"conversational",
		"uses slang",
	}
	return containsAny(lower, markers)
}

func isBetterThanExisting(oldText, newText string) bool {
	oldNorm := normalizeListItem(oldText)
	newNorm := normalizeListItem(newText)
	if oldNorm == "" || newNorm == "" {
		return false
	}
	if oldNorm == newNorm {
		return false
	}
	if strings.Contains(oldNorm, newNorm) && len(newNorm) < len(oldNorm) {
		return false
	}
	if looksLikeStyleInference(newText) {
		return false
	}
	if containsConcreteSignal(oldText) && !containsConcreteSignal(newText) {
		return false
	}
	if len(strings.Fields(newText)) < len(strings.Fields(oldText))/2 {
		return false
	}
	return true
}

func isWeakerThanExisting(existing []string, candidate string) bool {
	candidateNorm := normalizeListItem(candidate)
	if candidateNorm == "" {
		return true
	}
	for _, item := range existing {
		norm := normalizeListItem(item)
		if norm == candidateNorm {
			return true
		}
		if strings.Contains(norm, candidateNorm) && len(norm) > len(candidateNorm) {
			return true
		}
		if containsConcreteSignal(item) && !containsConcreteSignal(candidate) && sharesGenericTopic(norm, candidateNorm) {
			return true
		}
	}
	return false
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

func sanitizeDeltaSection(ops []DeltaOp, userName string) []DeltaOp {
	if len(ops) == 0 {
		return nil
	}
	cleaned := make([]DeltaOp, 0, len(ops))
	for _, op := range ops {
		op.NewText = sanitizePersonBullet(op.NewText, userName)
		if op.Action == '+' {
			op.Text = op.NewText
		} else if op.Action == '~' {
			op.Text = strings.TrimSpace(op.OldText) + " -> " + op.NewText
		}
		cleaned = append(cleaned, op)
	}
	return cleaned
}

func sanitizePersonBullet(text, userName string) string {
	text = strings.TrimSpace(text)
	if text == "" || strings.TrimSpace(userName) == "" {
		return text
	}
	prefixes := []string{
		userName + " ",
		strings.ToLower(userName) + " ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimSpace(text[len(prefix):])
			break
		}
	}
	if text == "" {
		return text
	}
	r, size := utf8.DecodeRuneInString(text)
	if r == utf8.RuneError && size == 0 {
		return text
	}
	return string(unicode.ToUpper(r)) + text[size:]
}

func containsConcreteSignal(text string) bool {
	for _, token := range strings.Fields(text) {
		trimmed := strings.Trim(token, ",.;:!?()[]{}\"'")
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, "0123456789") {
			return true
		}
		if strings.Contains(trimmed, ":") || strings.Contains(trimmed, "/") || strings.Contains(trimmed, "-") {
			return true
		}
		if len(trimmed) >= 3 && trimmed != strings.ToLower(trimmed) {
			return true
		}
	}
	return false
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func sharesGenericTopic(existing, candidate string) bool {
	topics := []string{"game", "games", "anime", "music", "movie", "movies", "stream", "streams"}
	for _, topic := range topics {
		if strings.Contains(existing, topic) && strings.Contains(candidate, topic) {
			return true
		}
	}
	return false
}
