package facts

import "strings"

type Evaluation struct {
	Value     string
	Accepted  bool
	Retryable bool
	Reason    string
}

var dossierHeadings = []string{
	"Identity:",
	"Interests:",
	"Relationships:",
}

var chatPromptHeadings = []string{
	"Recurring topics:",
	"Group dynamics:",
	"Communication norms:",
}

var suspiciousMetaMarkers = []string{
	"обновление фактов",
	"новых устойчивых фактов не появилось",
	"не добавляет долговременной информации",
	"текущее сообщение",
	"current facts:",
	"recent messages from",
	"no new stable facts",
	"does not add durable information",
	"facts update",
	"updated facts",
	"no durable information",
}

func NormalizePersonFacts(currentFacts, candidate string) string {
	return EvaluatePersonFacts(currentFacts, candidate).Value
}

func EvaluatePersonFacts(currentFacts, candidate string) Evaluation {
	current := normalizeWhitespace(currentFacts)
	next := normalizeWhitespace(candidate)

	if next == "" {
		return rejectedFacts(current, true, "empty output")
	}

	if looksLikeMetaFactsOutput(next) {
		return rejectedFacts(current, true, "meta output")
	}

	if !hasExpectedDossierStructure(next) {
		return rejectedFacts(current, true, "invalid dossier structure")
	}

	if informativeBulletCount(next) == 0 {
		return rejectedFacts(current, true, "no informative bullets")
	}

	if isSuspiciouslyShortFactsUpdate(current, next) {
		return rejectedFacts(current, true, "suspiciously short update")
	}

	if isSuspiciouslyDestructiveStructuredUpdate(current, next) {
		return rejectedFacts(current, true, "destructive rewrite")
	}

	return Evaluation{Value: next, Accepted: true}
}

func rejectedFacts(current string, retryable bool, reason string) Evaluation {
	return Evaluation{Value: current, Retryable: retryable, Reason: reason}
}

func normalizeWhitespace(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.TrimSpace(value)
}

func looksLikeMetaFactsOutput(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range suspiciousMetaMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasExpectedDossierStructure(value string) bool {
	lines := strings.Split(value, "\n")
	headingIndex := 0
	inSection := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if headingIndex < len(dossierHeadings) && line == dossierHeadings[headingIndex] {
			headingIndex++
			inSection = true
			continue
		}

		if isKnownHeading(line) {
			return false
		}

		if !inSection {
			return false
		}

		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			return false
		}
	}

	return headingIndex == len(dossierHeadings)
}

func isKnownHeading(line string) bool {
	for _, heading := range dossierHeadings {
		if line == heading {
			return true
		}
	}
	return false
}

func informativeBulletCount(value string) int {
	count := 0
	for _, rawLine := range strings.Split(value, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		content := strings.TrimSpace(line[2:])
		if content == "" || isPlaceholderBullet(content) {
			continue
		}
		count++
	}
	return count
}

func isPlaceholderBullet(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "none noted", "none yet", "unknown", "unknown yet", "n/a", "no durable facts yet":
		return true
	default:
		return false
	}
}

func isSuspiciouslyShortFactsUpdate(current, next string) bool {
	if current == "" || len(current) < 200 {
		return false
	}
	if len(next)*2 >= len(current) {
		return false
	}

	nonEmptyLines := 0
	structuredLines := 0
	for _, line := range strings.Split(next, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		nonEmptyLines++
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			structuredLines++
		}
	}

	return nonEmptyLines <= 3 && structuredLines == 0
}

func isSuspiciouslyDestructiveStructuredUpdate(current, next string) bool {
	currentBullets := informativeBulletCount(current)
	nextBullets := informativeBulletCount(next)
	if currentBullets < 4 {
		return false
	}
	return nextBullets*2 < currentBullets
}
