package spotify

import (
	"strings"
	"testing"
)

func TestSpotifyReviewWordCountExcludesStandalonePunctuation(t *testing.T) {
	if got := spotifyReviewWordCount("Вот — музыка. !!! 8,5 / 10 _"); got != 5 {
		t.Fatalf("word count = %d, want 5", got)
	}
}

func TestSpotifyJudgeHasExplicitJSONContract(t *testing.T) {
	// Codex does not serialize ResponseFormat; the prose must carry the contract.
	if !strings.Contains(spotifyReviewJudgeInstructions, `{"selected_id":"candidate_1","reason":"Why this candidate is best","rejected_ids":[]}`) {
		t.Fatal("missing JSON example")
	}
}
