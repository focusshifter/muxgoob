package facts

import "testing"

func TestParseRenderDossierRoundTrip(t *testing.T) {
	input := "Identity:\n- works in IT\n\nInterests:\n- likes metal\n- follows games\n\nRelationships:\n- knows friends from the chat"
	parsed := ParseDossier(input)
	got := RenderDossier(parsed)
	if got != input {
		t.Fatalf("expected roundtrip dossier, got %q", got)
	}
}

func TestParseDossierIgnoresUnknownHeadings(t *testing.T) {
	input := "Identity:\n- works in IT\n\nCommunication style:\n- sarcastic\n\nRelationships:\n- knows friends from the chat"
	parsed := ParseDossier(input)
	if len(parsed.Identity) != 1 || len(parsed.Relationships) != 1 || len(parsed.Interests) != 0 {
		t.Fatalf("unexpected parsed dossier: %#v", parsed)
	}
}
