package facts

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeDossierPreservesFactsBeyondLegacyBudgets(t *testing.T) {
	d := &Dossier{}
	for i := 0; i < 64; i++ {
		d.Identity = append(d.Identity, fmt.Sprintf("Specific identity detail number %d", i))
		d.Interests = append(d.Interests, fmt.Sprintf("Specific independent interest number %d", i))
		d.Appearance = append(d.Appearance, fmt.Sprintf("Canonical appearance element number %d", i))
	}
	got := NormalizeDossier(d)
	if !reflect.DeepEqual(got, d) {
		t.Fatalf("normalization lost or merged facts: identity=%d interests=%d appearance=%d", len(got.Identity), len(got.Interests), len(got.Appearance))
	}
	got.Identity[0] = "changed copy"
	if d.Identity[0] == got.Identity[0] {
		t.Fatal("normalization aliases caller's input")
	}
}

func TestNormalizeDossierOnlyRemovesExactDuplicates(t *testing.T) {
	items := []string{
		"Uses the same profile for work but not for private conversations",
		"Uses the same profile for work and for private conversations",
		"Uses the same profile for work",
		"API key names are TOKEN and token",
		"API key names are token and TOKEN",
	}
	input := append(append([]string(nil), items...), "  "+items[0]+"  ", " ")
	got := NormalizeDossier(&Dossier{Identity: input})
	if !reflect.DeepEqual(got.Identity, items) {
		t.Fatalf("nonidentical facts were merged: %#v", got.Identity)
	}
}

func TestNormalizeStoredPersonFactsRoundTrip(t *testing.T) {
	d := &Dossier{}
	for i := 0; i < 50; i++ {
		d.Identity = append(d.Identity, fmt.Sprintf("Identity marker %d is retained", i))
		d.Interests = append(d.Interests, fmt.Sprintf("Interest marker %d is retained", i))
	}
	text := RenderDossier(d)
	if got := NormalizeStoredPersonFacts(text); got != text {
		t.Fatal("stored dossier was truncated or rewritten")
	}
	opaque := " Legacy unstructured facts retain their exact internal wording. "
	if got := NormalizeStoredPersonFacts(opaque); got != strings.TrimSpace(opaque) {
		t.Fatalf("opaque legacy facts changed: %q", got)
	}
}

func TestNormalizeChatPromptPreservesGuidanceBeyondLegacyBudgets(t *testing.T) {
	p := &ChatPrompt{}
	for i := 0; i < 20; i++ {
		p.ReplyStyle = append(p.ReplyStyle, fmt.Sprintf("Style instruction %d", i))
		p.StableContext = append(p.StableContext, fmt.Sprintf("Stable context %d", i))
		p.Avoid = append(p.Avoid, fmt.Sprintf("Avoid instruction %d", i))
	}
	if got := NormalizeChatPrompt(p); !reflect.DeepEqual(got, p) {
		t.Fatalf("chat guidance was clipped: %#v", got)
	}
	p.Avoid = append(p.Avoid, " "+p.Avoid[0]+" ")
	if got := NormalizeChatPrompt(p); len(got.Avoid) != 20 {
		t.Fatal("exact guidance duplicate was not removed")
	}
}

func TestLosslessNormalizersPreserveColonTerminatedBullets(t *testing.T) {
	for _, prefix := range []string{"- ", "* "} {
		t.Run(prefix, func(t *testing.T) {
			input := "Identity:\n" + prefix + "Preferred literal prefix is user:\n" + prefix + "Lives in Helsinki\n\nAppearance:\n- Wears rectangular glasses\n\nInterests:\n- Enjoys hiking"
			dossier := ParseDossier(NormalizeStoredPersonFacts(input))
			if len(dossier.Identity) != 2 || dossier.Identity[0] != "Preferred literal prefix is user:" || dossier.Identity[1] != "Lives in Helsinki" {
				t.Fatalf("colon terminated fact or subsequent fact lost: %#v", dossier)
			}
			chat := "Reply style:\n" + prefix + "Prefix code examples with user:\n" + prefix + "Keep replies grounded\n\nStable context:\n- Telegram bot engineering\n\nAvoid:\n- Do not disclose secrets"
			prompt := NormalizeChatPrompt(ParseChatPrompt(chat))
			if len(prompt.ReplyStyle) != 2 || prompt.ReplyStyle[0] != "Prefix code examples with user:" || prompt.ReplyStyle[1] != "Keep replies grounded" {
				t.Fatalf("colon terminated guidance or subsequent guidance lost: %#v", prompt)
			}
		})
	}
}
