package facts

import "testing"

func TestParseDelta(t *testing.T) {
	input := "Identity:\n+ lives in Berlin\n\nInterests:\n~ likes games -> likes RPGs and MMOs"
	delta, err := ParseDelta(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(delta.Identity) != 1 || len(delta.Interests) != 1 {
		t.Fatalf("unexpected delta: %#v", delta)
	}
}

func TestParseDeltaRejectsInvalidLine(t *testing.T) {
	_, err := ParseDelta("Identity:\nthis is wrong")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestApplyDeltaAddsAndUpdates(t *testing.T) {
	current := &Dossier{
		Identity:      []string{"works in IT"},
		Interests:     []string{"likes games"},
		Relationships: []string{"knows friends from the chat"},
	}
	delta := &Delta{
		Identity:  []DeltaOp{{Action: '+', NewText: "lives in Berlin"}},
		Interests: []DeltaOp{{Action: '~', OldText: "likes games", NewText: "likes RPGs and MMOs"}},
	}
	merged := ApplyDelta(current, delta)
	if len(merged.Identity) != 2 {
		t.Fatalf("expected identity add, got %#v", merged.Identity)
	}
	if merged.Interests[0] != "likes RPGs and MMOs" {
		t.Fatalf("expected interest update, got %#v", merged.Interests)
	}
}

func TestApplyDeltaSkipsDuplicates(t *testing.T) {
	current := &Dossier{Identity: []string{"works in IT"}}
	delta := &Delta{Identity: []DeltaOp{{Action: '+', NewText: "works in IT"}}}
	merged := ApplyDelta(current, delta)
	if len(merged.Identity) != 1 {
		t.Fatalf("expected duplicate to be skipped, got %#v", merged.Identity)
	}
}

func TestIsNoChanges(t *testing.T) {
	if !IsNoChanges(" NO_CHANGES ") {
		t.Fatal("expected NO_CHANGES to be detected")
	}
	if IsNoChanges("Identity:\n+ works in IT") {
		t.Fatal("did not expect delta to count as no changes")
	}
}

func TestFilterDeltaForDossierRejectsWeakBullets(t *testing.T) {
	current := &Dossier{
		Identity:      []string{"Works in IT"},
		Interests:     []string{"Plays Diablo III"},
		Relationships: []string{"Has a friend named Kaz"},
	}
	delta := &Delta{
		Identity:      []DeltaOp{{Action: '+', NewText: "Tetra5 appears to have a light-hearted approach to conversations"}},
		Interests:     []DeltaOp{{Action: '+', NewText: "likes games"}},
		Relationships: []DeltaOp{{Action: '+', NewText: "interacts with Dima in chat"}},
	}

	filtered := FilterDeltaForDossier(current, delta)
	if len(filtered.Identity) != 0 || len(filtered.Interests) != 0 || len(filtered.Relationships) != 0 {
		t.Fatalf("expected all weak bullets to be filtered, got %#v", filtered)
	}
}

func TestFilterDeltaForDossierRejectsWeakerUpdate(t *testing.T) {
	current := &Dossier{
		Interests: []string{"Plays Wordle regularly"},
	}
	delta := &Delta{
		Interests: []DeltaOp{{Action: '~', OldText: "Plays Wordle regularly", NewText: "enjoys puzzles"}},
	}

	filtered := FilterDeltaForDossier(current, delta)
	if len(filtered.Interests) != 0 {
		t.Fatalf("expected weaker update to be filtered, got %#v", filtered.Interests)
	}
}

func TestFilterDeltaForDossierAllowsMoreSpecificUpdate(t *testing.T) {
	current := &Dossier{
		Identity: []string{"likes keyboards"},
	}
	delta := &Delta{
		Identity: []DeltaOp{{Action: '~', OldText: "likes keyboards", NewText: "Prefers Cherry MX blue switches"}},
	}

	filtered := FilterDeltaForDossier(current, delta)
	if len(filtered.Identity) != 1 {
		t.Fatalf("expected specific update to survive filtering, got %#v", filtered.Identity)
	}
}
