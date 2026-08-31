package facts

import (
	"strings"
	"testing"
)

func TestParseRenderDossierRoundTrip(t *testing.T) {
	input := "Identity:\n- works in IT\n\nInterests:\n- likes metal\n- follows games"
	parsed := ParseDossier(input)
	got := RenderDossier(parsed)
	if got != input {
		t.Fatalf("expected roundtrip dossier, got %q", got)
	}
}

func TestParseDossierIgnoresUnknownHeadings(t *testing.T) {
	input := "Identity:\n- works in IT\n\nCommunication style:\n- sarcastic\n\nRelationships:\n- knows friends from the chat"
	parsed := ParseDossier(input)
	if len(parsed.Identity) != 1 || len(parsed.Interests) != 0 {
		t.Fatalf("unexpected parsed dossier: %#v", parsed)
	}
}

func TestEnforcePersonFactsBudgetsDedupesRepeatedGeneratedSlop(t *testing.T) {
	input := `Identity:
- Uses meme-heavy, hostile, and very slangy chat as a habitual style.
- Uses vulgar, meme-heavy trash talk naturally in chat.

Interests:
- Interested in Forza and compares it to a Japan-like driving experience.
- Thinks Forza’s driving vibe feels like being in Japan.
- Interested in Forza and asks what others think about it.
- Likes the idea of a “gейхаус” / gaming house where PCs with Forza are set up in the basement and people just hang out with burgers.
- Interested in a “гейхаус”/gaming-house setup where PCs, Forza, and service/snack delivery are in the basement.
- Strongly dislikes Smute and uses it as a recurring insult/meme target.
- Strongly dislikes Smute and now uses it as a recurring insult/meme target.
- Uses “СМУТЕ” as a recurring insult/meme target in chat.
- Hates Albion Online openly and treats it as a recurring target of criticism.
- Actively hates on Albion Online.
- Enjoys sharing and discussing YouTube gameplay clips with timestamped commentary.
- Still follows YouTube clips with timestamped gameplay/mixup commentary and enjoys micro-analysis.
- Uses “kazyan in 2026” as a recurring future-joke reference.
- Likes using “в 2026” as a future-joke reference for Kazyan.
- Mentions springtime running in the forest / garden work as a recurring seasonal activity.
- References springtime outdoor runs / going to the forest as a recurring seasonal activity.`

	got := EnforcePersonFactsBudgets(input)
	if strings.Count(got, "Forza") > 2 {
		t.Fatalf("expected Forza duplicates to be compacted, got:\n%s", got)
	}
	if strings.Count(strings.ToLower(got), "smute")+strings.Count(strings.ToLower(got), "смут") > 2 {
		t.Fatalf("expected Smute duplicates to be compacted, got:\n%s", got)
	}
	if strings.Count(strings.ToLower(got), "albion") > 1 {
		t.Fatalf("expected Albion duplicates to be compacted, got:\n%s", got)
	}
	if countBullets(got) > MaxDossierIdentityBullets+MaxDossierInterestBullets {
		t.Fatalf("expected budgeted facts, got %d bullets:\n%s", countBullets(got), got)
	}
}

func TestAppearanceFactsSurviveAutomaticDossierCompactionAndDelta(t *testing.T) {
	current := ParseDossier(`Appearance:
- Has a van-dyke beard and rectangular glasses
- Wears a dark hoodie

Identity:
- works in IT

Interests:
- likes games`)
	delta := &Delta{Interests: []DeltaOp{{Action: '+', Text: "likes XCOM", NewText: "likes XCOM"}}}
	got := EnforceDossierBudgets(ApplyDelta(current, delta))
	if len(got.Appearance) != 2 || got.Appearance[0] != "Has a van-dyke beard and rectangular glasses" || got.Appearance[1] != "Wears a dark hoodie" {
		t.Fatalf("appearance facts must survive automatic updates unchanged: %#v", got.Appearance)
	}
	if !strings.Contains(RenderDossier(got), "Appearance:\n- Has a van-dyke beard and rectangular glasses") {
		t.Fatalf("appearance section missing from rendered dossier: %q", RenderDossier(got))
	}
}

func countBullets(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			count++
		}
	}
	return count
}
