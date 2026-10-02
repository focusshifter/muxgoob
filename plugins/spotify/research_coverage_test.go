package spotify

import (
	"encoding/json"
	"strings"
	"testing"
)

func encodeResearchFixture(t *testing.T, sources []spotifyCriticism, citations []researchCitation) researchResponse {
	t.Helper()
	data, err := json.Marshal(spotifyResearch{Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	return researchResponse{Content: string(data), Citations: citations}
}

func TestAlbumCoverageRejectsActualLivePreviewFailures(t *testing.T) {
	for _, tc := range []struct{ url, quote, track string }{
		{"https://post-punk.com/kontravoid-announces-new-album-sound-of-the-void-listen-to-new-track-without/", "widening the project’s vocabulary while keeping its identity intact", "Without"},
		{"https://post-punk.com/kontravoid-severs-expectations-with-sinister-synthpop-single-chances/", "Findlay’s second full-length gives melody more room", "Chances"},
	} {
		// Replay the actual failure shape: model labels a single/preview page album,
		// with plausible praise and provider citation membership.
		s := spotifyCriticism{Publication: "Post-Punk.com", URL: tc.url, Subject: "Sound of the Void", Scope: "album", Coverage: "full_album_appraisal", Praise: "The record expands its vocabulary and gives melody more room.", EvidenceQuote: tc.quote}
		resp := encodeResearchFixture(t, []spotifyCriticism{s}, []researchCitation{{URL: tc.url}})
		got, _ := parseSpotifyResearch(resp, "album", []string{"Sound of the Void"})
		if got.hasAlbumEvidence() {
			t.Fatalf("preview awarded album evidence: %s", tc.url)
		}
		if _, rating, err := parseSpotifyReviewCompletion(`{"review_text":"Album verdict","album_rating":7}`, "album"); err != nil || !rating.Valid {
			t.Fatal("album score incorrectly gated by research coverage")
		}
		// Never relabel the rejected model entry automatically; the single pass must
		// supply its own song-specific appraisal, still usable on these same pages.
		s.Subject = tc.track
		s.Scope = "single"
		s.Coverage = "track_appraisal"
		s.Praise = "Melody and rhythmic force work together in this named song."
		resp = encodeResearchFixture(t, []spotifyCriticism{s}, resp.Citations)
		got, err := parseSpotifyResearch(resp, "single", []string{tc.track})
		if err != nil || len(got.Sources) != 1 {
			t.Fatalf("lost single editorial: %+v %v", got, err)
		}
	}
}

func TestAlbumCitationMetadataConservativeCoverage(t *testing.T) {
	for _, tc := range []struct {
		url, title string
		want       bool
	}{
		{"https://critic.example/123", "Artist: Album Review", true},
		{"https://critic.example/123", "Artist announces an album", false},
		{"https://critic.example/review", "Album preview: a single review", false},
		{"https://critic.example/announces-new-track", "Album Review", false},
		{"https://critic.example/album-review", "", true},
		{"https://critic.example/123", "Unclassified editorial", false},
		{"https://critic.example/%73ingle-review", "Album Review", false},
		{"https://critic.example/different-release-review", "Different Release: Review", false},
		{"https://critic.example/review?album=Album", "Different Release: Review", false},
	} {
		citations := []researchCitation{{URL: tc.url}, {URL: tc.url, Title: tc.title}}
		if got := albumCitationCoverage(tc.url, "Album", citations); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}

func TestResearchMixedValidInvalidEntries(t *testing.T) {
	fixture := criticismFixture("Sound of the Void", "album")
	var r spotifyResearch
	if err := json.Unmarshal([]byte(fixture.Content), &r); err != nil {
		t.Fatal(err)
	}
	valid := r.Sources[0]
	for _, kind := range []string{"empty", "oversized", "provenance", "preview_coverage"} {
		invalid := valid
		switch kind {
		case "empty":
			invalid.Publication = ""
		case "oversized":
			invalid.Praise = strings.Repeat("x", 1601)
		case "provenance":
			invalid.Provenance = "verified"
		case "preview_coverage":
			invalid.Coverage = "release_anticipation"
		}
		resp := encodeResearchFixture(t, []spotifyCriticism{valid, invalid}, fixture.Citations)
		got, err := parseSpotifyResearch(resp, "album", []string{valid.Subject})
		if err != nil || len(got.Sources) != 1 {
			t.Fatalf("%s sibling discarded valid appraisal: %+v %v", kind, got, err)
		}
	}
	single := valid
	single.Subject = "Without"
	single.Scope = "single"
	single.Coverage = "track_appraisal"
	resp := encodeResearchFixture(t, []spotifyCriticism{valid, single}, fixture.Citations)
	got, _ := parseSpotifyResearch(resp, "album", []string{valid.Subject})
	if got.hasAlbumEvidence() {
		t.Fatal("same-source mixed scope awarded album evidence")
	}
	got, err := parseSpotifyResearch(resp, "single", []string{single.Subject})
	if err != nil || len(got.Sources) != 1 {
		t.Fatal("same-source mixed scope discarded single appraisal")
	}
	malformedSibling := resp
	malformedSibling.Content = strings.Replace(resp.Content, `"coverage":"track_appraisal",`, "", 1)
	got, _ = parseSpotifyResearch(malformedSibling, "album", []string{valid.Subject})
	if got.hasAlbumEvidence() {
		t.Fatal("filtering malformed single erased same-source scope conflict")
	}
	single.URL = "https://critic.example/another-single"
	resp = encodeResearchFixture(t, []spotifyCriticism{valid, single}, append(fixture.Citations, researchCitation{URL: single.URL}))
	got, err = parseSpotifyResearch(resp, "album", []string{valid.Subject})
	if err != nil || !got.hasAlbumEvidence() || len(got.Sources) != 1 {
		t.Fatal("different-source single discarded valid album appraisal")
	}
}
