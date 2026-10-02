package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func criticismFixture(subject, scope string) researchResponse {
	r := spotifyResearch{Sources: []spotifyCriticism{{Publication: "Example Critic", URL: "https://critic.example/review", Subject: subject, Scope: scope, Coverage: map[string]string{"album": "full_album_appraisal", "single": "track_appraisal"}[scope], Praise: "The double-time chorus expands the vocal approach.", Criticism: "", EvidenceQuote: "especially on the double-time chorus"}}}
	if scope == "album" {
		r.Sources[0].URL = "https://critic.example/" + strings.ReplaceAll(strings.ToLower(subject), " ", "-") + "-review"
		r.Sources[0].Praise = "Across the full record, contrasting vocal arrangements and varied rhythms sustain its momentum."
		r.Sources[0].EvidenceQuote = "contrasting vocal arrangements and varied rhythms sustain its momentum"
	}
	data, _ := json.Marshal(r)
	return researchResponse{Content: string(data), Citations: []researchCitation{{URL: r.Sources[0].URL}}}
}
func researchAlbumFixture(t *testing.T) *SpotifyAlbum {
	t.Helper()
	var a SpotifyAlbum
	if err := json.Unmarshal([]byte(`{"tracks":{"items":[{"name":"Without"},{"name":"Chances"}]}}`), &a); err != nil {
		t.Fatal(err)
	}
	return &a
}
func TestResearchSpotifyCriticismBoundedPasses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first         researchResponse
		firstError    error
		typ           string
		album         bool
		calls         int
		sources       int
		albumEvidence bool
	}{
		{name: "album evidence stops", first: criticismFixture("Sound of the Void", "album"), typ: "album", album: true, calls: 1, sources: 1, albumEvidence: true},
		{name: "empty album falls back to named singles", first: researchResponse{Content: `{"sources":[]}`}, typ: "album", album: true, calls: 2, sources: 1},
		{name: "malformed album falls back", first: researchResponse{Content: "No reliable reviews exist yet."}, typ: "album", album: true, calls: 2, sources: 1},
		{name: "provider error falls back", firstError: errors.New("provider failure"), typ: "album", album: true, calls: 2, sources: 1},
		{name: "uncited album falls back", first: researchResponse{Content: criticismFixture("Sound of the Void", "album").Content}, typ: "album", album: true, calls: 2, sources: 1},
		{name: "no metadata no invented single search", first: researchResponse{Content: `{"sources":[]}`}, typ: "album", calls: 1},
		{name: "track only one pass", first: criticismFixture("Without", "single"), typ: "track", album: true, calls: 1, sources: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			search := func(ctx context.Context, q string) (researchResponse, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 25*time.Second {
					t.Fatal("missing bounded pass deadline")
				}
				calls++
				if calls == 1 {
					return tc.first, tc.firstError
				}
				if !strings.Contains(q, `["Without","Chances"]`) || !strings.Contains(q, "before the album year") {
					t.Fatalf("missing metadata-informed single query: %s", q)
				}
				return criticismFixture("Without", "single"), nil
			}
			var album *SpotifyAlbum
			if tc.album {
				album = researchAlbumFixture(t)
			}
			title := "Sound of the Void"
			if tc.typ == "track" {
				title = "Without"
			}
			got := researchSpotifyCriticism(context.Background(), search, tc.typ, "Kontravoid", title, "2026", album)
			if calls != tc.calls || len(got.Sources) != tc.sources || got.hasAlbumEvidence() != tc.albumEvidence {
				t.Fatalf("calls=%d research=%+v", calls, got)
			}
		})
	}
}
func TestParseSpotifyResearchValidation(t *testing.T) {
	good := criticismFixture("Without", "single")
	for _, content := range []string{
		"No reviews are available", "```json\n" + good.Content + "\n```", "{}", `{"sources":null}`, good.Content + " {}",
		strings.Replace(good.Content, `"scope":"single"`, `"scope":"album"`, 1),
		strings.Replace(good.Content, `"subject":"Without"`, `"subject":"Not on the album"`, 1),
		strings.Replace(good.Content, `"criticism":"",`, "", 1),
		strings.Replace(good.Content, `"criticism":""`, `"criticism":null`, 1),
		strings.Replace(good.Content, `"sources":`, `"essay":"not enough evidence","sources":`, 1),
		strings.Replace(good.Content, `"publication":`, `"provenance":"verified","publication":`, 1),
	} {
		r := good
		r.Content = content
		if _, err := parseSpotifyResearch(r, "single", []string{"Without"}); err == nil {
			t.Fatalf("accepted invalid research %s", content)
		}
	}
	r := good
	r.Citations = nil
	got, err := parseSpotifyResearch(r, "single", []string{"Without"})
	if err != nil || len(got.Sources) != 0 {
		t.Fatalf("uncited URL accepted: %+v %v", got, err)
	}
	r.Citations = []researchCitation{{URL: "https://critic.example/different-review"}}
	got, _ = parseSpotifyResearch(r, "single", []string{"Without"})
	if len(got.Sources) != 0 {
		t.Fatal("different provider URL accepted")
	}
	good.Citations[0].Content = "This page is only a release announcement."
	got, _ = parseSpotifyResearch(good, "single", []string{"Without"})
	if len(got.Sources) != 0 {
		t.Fatal("invented quote accepted despite available excerpt")
	}
	good.Citations = append(good.Citations, researchCitation{URL: good.Citations[0].URL})
	got, _ = parseSpotifyResearch(good, "single", []string{"Without"})
	if len(got.Sources) != 0 {
		t.Fatal("bare citation bypassed excerpt mismatch")
	}
	good.Citations[0].Content = "Vocal expansion, especially on the\n double-time chorus, is impressive."
	got, err = parseSpotifyResearch(good, "single", []string{"Without"})
	if err != nil || len(got.Sources) != 1 || !strings.Contains(got.Sources[0].Provenance, "provider_excerpt_contains_quote") {
		t.Fatalf("excerpt lost: %+v %v", got, err)
	}
	good.Citations[0].Content = ""
	got, _ = parseSpotifyResearch(good, "single", []string{"Without"})
	if !strings.Contains(got.Sources[0].Provenance, "not independently verified") {
		t.Fatal("URL membership claimed verification")
	}
}
func TestCriticismURLRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{"http://critic.example/review", "https://user:pass@critic.example/review", "https://localhost/a", "https://foo.local/a", "https://foo.internal/a", "https://127.0.0.1/a", "https://10.0.0.1/a", "https://169.254.169.254/a", "https://[::1]/a", "https://[fc00::1]/a", "file:///etc/passwd", "https://critic.example:8080/a", "https://critic.example/a#claim"} {
		if _, err := criticismURL(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	if _, err := criticismURL("https://post-punk.com/kontravoid-review/"); err != nil {
		t.Fatal(err)
	}
}

// These editorial observations were directly checked by the parent in a live
// browser; the production path still marks provider-only citations unverified.
// A fixture is not substituted for a live provider response or source retrieval.
func TestKontravoidEditorialFixturePreservesScopeAndDisagreement(t *testing.T) {
	r := spotifyResearch{Sources: []spotifyCriticism{
		{Publication: "I Die: You Die", URL: "https://www.idieyoudie.com/2026/09/08/tracks-september-8th-2026/", Subject: "Without", Scope: "single", Coverage: "track_appraisal", Praise: "Expanded vocal approach, especially the double-time chorus.", EvidenceQuote: "especially on the double-time chorus"},
		{Publication: "Post-Punk.com", URL: "https://post-punk.com/kontravoid-announces-new-album-sound-of-the-void-listen-to-new-track-without/", Subject: "Without", Scope: "single", Coverage: "track_appraisal", Praise: "Pulsing bass, alarm-like synths and precise heavy drums expand the vocabulary.", EvidenceQuote: "an alarm-like synthwave figure"},
		{Publication: "I Die: You Die", URL: "https://www.idieyoudie.com/2026/09/28/tracks-september-27th-2026/", Subject: "Chances", Scope: "single", Coverage: "track_appraisal", Praise: "Catchy, danceable, melancholic but forceful electro-pop.", EvidenceQuote: "catchy, danceable, melancholic but forceful"},
		{Publication: "Post-Punk.com", URL: "https://post-punk.com/kontravoid-severs-expectations-with-sinister-synthpop-single-chances/", Subject: "Chances", Scope: "single", Coverage: "track_appraisal", Praise: "Melody and hooks move forward without abandoning abrasive coldwave and EBM force.", EvidenceQuote: "Chances simply drags it forward"},
	}}
	data, _ := json.Marshal(r)
	resp := researchResponse{Content: string(data)}
	for _, s := range r.Sources {
		resp.Citations = append(resp.Citations, researchCitation{URL: s.URL})
	}
	got, err := parseSpotifyResearch(resp, "single", []string{"Without", "Chances"})
	if err != nil || len(got.Sources) != 4 || got.hasAlbumEvidence() {
		t.Fatalf("bad scoped fixture %+v %v", got, err)
	}
	if !strings.Contains(buildSpotifySinglesQuery("Kontravoid", "Sound of the Void", "2026", researchAlbumFixture(t)), "classify the passage, not the page headline") {
		t.Fatal("editorial announcement discarded")
	}
	// Synthetic opposing judgment tests preservation, not a claim about this album.
	opposing := r.Sources[0]
	opposing.Publication = "Synthetic opposing critic"
	opposing.Praise = ""
	opposing.Criticism = "The chorus is repetitive and the vocal delivery lacks variation."
	r.Sources = append(r.Sources, opposing)
	data, _ = json.Marshal(r)
	resp.Content = string(data)
	got, err = parseSpotifyResearch(resp, "single", []string{"Without", "Chances"})
	if err != nil || len(got.Sources) != 5 || !strings.Contains(got.render(), "lacks variation") || !strings.Contains(got.render(), "one editorial voice") {
		t.Fatalf("lost disagreement: %+v %v", got, err)
	}
}

type researchRoundTripper func(*http.Request) (*http.Response, error)

func (f researchRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRequestSpotifyResearchProviderCitations(t *testing.T) {
	good := criticismFixture("Without", "single")
	for _, kind := range []string{"annotations", "citations", "search_results"} {
		t.Run(kind, func(t *testing.T) {
			message := map[string]any{"content": good.Content}
			envelope := map[string]any{}
			switch kind {
			case "annotations":
				message[kind] = []any{map[string]any{"type": "url_citation", "url_citation": map[string]any{"url": good.Citations[0].URL, "content": "especially on the double-time chorus", "title": "Named track review"}}}
			case "citations":
				envelope[kind] = []string{good.Citations[0].URL}
			case "search_results":
				envelope[kind] = []any{map[string]any{"url": good.Citations[0].URL, "title": "Named track review"}}
			}
			envelope["choices"] = []any{map[string]any{"finish_reason": "stop", "message": message}}
			body, _ := json.Marshal(envelope)
			calls := 0
			client := &http.Client{Transport: researchRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != "https://openrouter.ai/api/v1/chat/completions" || req.Method != "POST" {
					t.Fatalf("arbitrary source fetched: %s", req.URL)
				}
				var payload map[string]any
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["max_tokens"] != float64(2400) || payload["model"] != "perplexity/sonar" {
					t.Fatalf("bad payload %+v", payload)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}
			resp, err := requestSpotifyResearch(context.Background(), client, "test-key", "query")
			if err != nil {
				t.Fatal(err)
			}
			if kind != "citations" && (len(resp.Citations) == 0 || resp.Citations[0].Title != "Named track review") {
				t.Fatal("provider citation title discarded")
			}
			got, err := parseSpotifyResearch(resp, "single", []string{"Without"})
			if err != nil || len(got.Sources) != 1 || calls != 1 {
				t.Fatalf("citation decode failed: %+v %v calls %d", got, err, calls)
			}
		})
	}
}
func TestRequestSpotifyResearchRejectsIncompleteAndOversized(t *testing.T) {
	for _, body := range []string{`{"choices":[]}`, `{"choices":[{"finish_reason":"length","message":{"content":"truncated"}}]}`, strings.Repeat("x", (1<<20)+1)} {
		client := &http.Client{Transport: researchRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		if _, err := requestSpotifyResearch(context.Background(), client, "key", "query"); err == nil {
			t.Fatal("accepted incomplete/oversized response")
		}
	}
}
func TestSinglesEvidenceProducesRatedAlbumReview(t *testing.T) {
	prompt := buildSpotifyReviewPrompt("album", "Kontravoid", "Sound of the Void", "2026", criticismFixture("Without", "single").Content)
	for _, want := range []string{"Give the album a numeric rating", "review of the ALBUM", "album-wide thesis", "never infer sound", "not as two separate mini-reviews", "Do not claim first-hand listening"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, banned := range []string{"album_rating=null", "Attribute each publication", "focused critical column", "Do not give an album-wide score"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("obsolete gate %q", banned)
		}
	}
	text, rating, err := parseSpotifyReviewCompletion(`{"review_text":"Рецензия на альбом с собственным тезисом.","album_rating":6.5}`, "album")
	if err != nil || !rating.Valid || rating.Float64 != 6.5 || !strings.HasSuffix(text, "6,5 / 10") {
		t.Fatalf("%q %+v %v", text, rating, err)
	}
	if _, _, err := parseSpotifyReviewCompletion(`{"review_text":"Рецензия.","album_rating":null}`, "album"); err == nil {
		t.Fatal("album requires rating even with single-only grounding")
	}
	text, rating, err = parseSpotifyReviewCompletion(`{"review_text":"Рецензия на трек.","album_rating":null}`, "track")
	if err != nil || rating.Valid || text != "Рецензия на трек." {
		t.Fatalf("%q %+v %v", text, rating, err)
	}
	if _, _, err := parseSpotifyReviewCompletion(`{"review_text":"Трек.","album_rating":8}`, "track"); err == nil {
		t.Fatal("track score accepted")
	}
}

func TestResearchCancellationDoesNotStartNewPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	search := func(context.Context, string) (researchResponse, error) {
		calls++
		return researchResponse{Content: `{"sources":[]}`}, nil
	}
	got := researchSpotifyCriticism(ctx, search, "album", "Artist", "Title", "2026", researchAlbumFixture(t))
	if calls != 0 || len(got.Sources) != 0 {
		t.Fatal("canceled research performed network work")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	search = func(context.Context, string) (researchResponse, error) {
		calls++
		cancel()
		return researchResponse{Content: `{"sources":[]}`}, nil
	}
	_ = researchSpotifyCriticism(ctx, search, "album", "Artist", "Title", "2026", researchAlbumFixture(t))
	if calls != 1 {
		t.Fatalf("canceled first pass still started fallback: %d", calls)
	}
}
func TestParseReviewRejectsMissingAndExtraFields(t *testing.T) {
	for _, content := range []string{`{"review_text":"Text"}`, `{"review_text":"Text","album_rating":null,"extra":"claim"}`, `{"review_text":"Text","album_rating":null} {}`, "not JSON"} {
		if _, _, err := parseSpotifyReviewCompletion(content, "album"); err == nil {
			t.Fatalf("accepted malformed structured review %s", content)
		}
	}
}
