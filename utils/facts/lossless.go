package facts

import "strings"

// NormalizeDossier removes exact duplicate bullets without imposing section
// budgets or treating overlapping wording as interchangeable facts.
func NormalizeDossier(d *Dossier) *Dossier {
	if d == nil {
		return &Dossier{}
	}
	return &Dossier{
		Identity:   exactUniqueBullets(d.Identity),
		Appearance: exactUniqueBullets(d.Appearance),
		Interests:  exactUniqueBullets(d.Interests),
	}
}

// NormalizeStoredPersonFacts preserves opaque legacy text and all distinct
// structured facts. Lossy context budgets must not be applied to stored memory.
func NormalizeStoredPersonFacts(text string) string {
	text = normalizeWhitespace(text)
	if text == "" || !hasExpectedDossierStructure(text) {
		return text
	}
	return RenderDossier(NormalizeDossier(ParseDossier(text)))
}

// NormalizeChatPrompt retains all distinct guidance, including sections beyond
// the legacy display budgets. Automatic consolidation has a separate trigger.
func NormalizeChatPrompt(prompt *ChatPrompt) *ChatPrompt {
	if prompt == nil {
		return &ChatPrompt{}
	}
	return &ChatPrompt{
		ReplyStyle:    exactUniqueBullets(prompt.ReplyStyle),
		StableContext: exactUniqueBullets(prompt.StableContext),
		Avoid:         exactUniqueBullets(prompt.Avoid),
	}
}

func exactUniqueBullets(items []string) []string {
	var result []string
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, duplicate := seen[item]; duplicate {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
