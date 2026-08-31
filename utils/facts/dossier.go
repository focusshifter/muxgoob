package facts

import (
	"sort"
	"strings"
	"unicode"
)

const (
	MaxDossierIdentityBullets   = 12
	MaxDossierAppearanceBullets = 12
	MaxDossierInterestBullets   = 32
	MaxDossierCanonicalTokens   = 8
)

type Dossier struct {
	// Appearance is a user-identity anchor (visual traits, canonical depiction,
	// clothing). It is intentionally excluded from automated delta updates and
	// budget eviction; only an explicit owner/user memory mutation may change it.
	Appearance []string
	Identity   []string
	Interests  []string
}

func ParseDossier(text string) *Dossier {
	d := &Dossier{}
	var section *[]string

	for _, rawLine := range strings.Split(normalizeWhitespace(text), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		switch line {
		case "Appearance:":
			section = &d.Appearance
			continue
		case "Identity:":
			section = &d.Identity
			continue
		case "Interests:":
			section = &d.Interests
			continue
		}

		if strings.HasSuffix(line, ":") {
			section = nil
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

	return d
}

func RenderDossier(d *Dossier) string {
	if d == nil {
		d = &Dossier{}
	}

	var out strings.Builder
	writeSection(&out, "Identity:", d.Identity)
	if len(d.Appearance) > 0 {
		out.WriteString("\n\n")
		writeSection(&out, "Appearance:", d.Appearance)
	}
	out.WriteString("\n\n")
	writeSection(&out, "Interests:", d.Interests)
	return strings.TrimSpace(out.String())
}

func EnforcePersonFactsBudgets(text string) string {
	text = normalizeWhitespace(text)
	if text == "" {
		return ""
	}
	if !hasExpectedDossierStructure(text) {
		return text
	}
	return RenderDossier(EnforceDossierBudgets(ParseDossier(text)))
}

func EnforceDossierBudgets(d *Dossier) *Dossier {
	if d == nil {
		return &Dossier{}
	}
	return &Dossier{
		Appearance: preserveAppearanceFacts(d.Appearance),
		Identity:   compactDossierSection(d.Identity, MaxDossierIdentityBullets),
		Interests:  compactDossierSection(d.Interests, MaxDossierInterestBullets),
	}
}

func preserveAppearanceFacts(items []string) []string {
	kept := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		duplicate := false
		for _, existing := range kept {
			if strings.EqualFold(existing, item) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			if len(kept) >= MaxDossierAppearanceBullets {
				continue
			}
			kept = append(kept, item)
		}
	}
	return kept
}

func writeSection(out *strings.Builder, heading string, items []string) {
	out.WriteString(heading)
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out.WriteString("\n- ")
		out.WriteString(item)
	}
}

func compactDossierSection(items []string, limit int) []string {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	compacted := make([]string, 0, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" || isPlaceholderBullet(item) {
			continue
		}
		itemKey := dossierBulletKey(item)
		if itemKey == "" {
			continue
		}

		duplicateAt := -1
		for i, existing := range compacted {
			if dossierBulletsOverlap(itemKey, dossierBulletKey(existing)) {
				duplicateAt = i
				break
			}
		}
		if duplicateAt < 0 {
			compacted = append(compacted, item)
			continue
		}
		if betterDossierBullet(item, compacted[duplicateAt]) {
			compacted[duplicateAt] = item
		}
	}
	if len(compacted) > limit {
		compacted = compacted[:limit]
	}
	return compacted
}

func dossierBulletsOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b || strings.Contains(a, b) || strings.Contains(b, a) {
		return true
	}
	at := strings.Fields(a)
	bt := strings.Fields(b)
	if len(at) == 0 || len(bt) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(at))
	for _, token := range at {
		set[token] = struct{}{}
	}
	shared := 0
	for _, token := range bt {
		if _, ok := set[token]; ok {
			shared++
		}
	}
	shorter := len(at)
	if len(bt) < shorter {
		shorter = len(bt)
	}
	return shorter > 0 && shared*100/shorter >= 55
}

func betterDossierBullet(candidate, existing string) bool {
	c := strings.TrimSpace(candidate)
	e := strings.TrimSpace(existing)
	if c == "" {
		return false
	}
	if e == "" {
		return true
	}
	cConcrete := containsConcreteSignal(c)
	eConcrete := containsConcreteSignal(e)
	if cConcrete != eConcrete {
		return cConcrete
	}
	cWords := len(strings.Fields(c))
	eWords := len(strings.Fields(e))
	if cWords == eWords {
		return len(c) < len(e)
	}
	if cWords < 4 && eWords >= 4 {
		return false
	}
	return cWords < eWords
}

func dossierBulletKey(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		"“", " ", "”", " ", "\"", " ", "'", " ", "`", " ",
		"/", " ", "-", " ", "_", " ", "ё", "е",
	)
	lower = replacer.Replace(lower)
	var cleaned strings.Builder
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			cleaned.WriteRune(r)
		} else {
			cleaned.WriteRune(' ')
		}
	}
	tokens := make([]string, 0)
	seen := make(map[string]struct{})
	for _, token := range strings.Fields(cleaned.String()) {
		token = normalizeDossierToken(token)
		if token == "" || isDossierStopToken(token) {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return ""
	}
	if len(tokens) > MaxDossierCanonicalTokens {
		tokens = tokens[:MaxDossierCanonicalTokens]
	}
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

func normalizeDossierToken(token string) string {
	token = strings.TrimSpace(token)
	for _, suffix := range []string{"ing", "ed", "es", "s", "ами", "ями", "ах", "ях", "ого", "ему", "ыми", "ими", "ый", "ой", "ая", "ое", "ые", "ую"} {
		if len([]rune(token)) > len([]rune(suffix))+3 && strings.HasSuffix(token, suffix) {
			return strings.TrimSuffix(token, suffix)
		}
	}
	return token
}

func isDossierStopToken(token string) bool {
	if len([]rune(token)) <= 2 {
		return true
	}
	stops := map[string]struct{}{
		"interested": {}, "interest": {}, "interests": {}, "likes": {}, "like": {}, "enjoys": {}, "enjoy": {},
		"mentions": {}, "mention": {}, "references": {}, "reference": {}, "uses": {}, "use": {}, "still": {},
		"active": {}, "recurring": {}, "context": {}, "topic": {}, "setup": {}, "part": {}, "including": {},
		"views": {}, "feels": {}, "thinks": {}, "strongly": {}, "openly": {}, "recently": {}, "asks": {},
		"what": {}, "others": {}, "other": {}, "people": {}, "person": {},
		"the": {}, "and": {}, "with": {}, "for": {}, "from": {}, "about": {}, "as": {}, "of": {}, "to": {}, "in": {}, "it": {}, "is": {},
		"интересуется": {}, "любит": {}, "упоминает": {}, "использует": {}, "сильно": {}, "активный": {}, "активно": {},
		"как": {}, "про": {}, "для": {}, "это": {}, "или": {}, "что": {}, "еще": {}, "ещё": {},
	}
	_, ok := stops[token]
	return ok
}
