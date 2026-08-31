package facts

import (
	"strings"
	"unicode"
)

// PersonNamesEquivalent compares a single participant-name token with a
// Telegram name. Besides exact matching, it handles ordinary Russian case
// forms and a deliberately small, explicit set of Russian diminutive families
// with common Latin Telegram spellings. It does not search outside the caller's
// chat scope.
func PersonNamesEquivalent(request, candidate string) bool {
	request = canonicalPersonName(request)
	candidate = canonicalPersonName(candidate)
	return request != "" && candidate != "" && request == candidate
}

var canonicalRussianFirstNames = map[string]string{
	"виктор": "viktor",
	"витя":   "viktor",
	"витю":   "viktor",
	"вите":   "viktor",
	"витей":  "viktor",
	"витёк":  "viktor",
	"витек":  "viktor",
	"вик":    "viktor",
	"victor": "viktor",
	"viktor": "viktor",
	"vitya":  "viktor",
	"иван":   "ivan",
	"ivan":   "ivan",
}

func canonicalPersonName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if canonical, ok := canonicalRussianFirstNames[value]; ok {
		return canonical
	}
	if !containsCyrillic(value) {
		return value
	}
	stem := russianPersonNameStem(value)
	if canonical, ok := canonicalRussianFirstNames[stem]; ok {
		return canonical
	}
	return stem
}

func containsCyrillic(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return unicode.Is(unicode.Cyrillic, r)
	}) >= 0
}

func russianPersonNameStem(value string) string {
	for _, suffix := range []string{"ами", "ями", "ого", "ему", "ому", "ыми", "ими", "ом", "ем", "ах", "ях", "ой", "ей", "ам", "ям", "ов", "ев", "а", "я", "у", "ю", "ы", "и", "е"} {
		if strings.HasSuffix(value, suffix) {
			stem := strings.TrimSuffix(value, suffix)
			if len([]rune(stem)) >= 3 {
				return stem
			}
		}
	}
	return value
}
