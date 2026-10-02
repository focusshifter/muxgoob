package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
)

const maxResearchSources = 6

type spotifyCriticism struct {
	Publication   string `json:"publication"`
	URL           string `json:"url"`
	Subject       string `json:"subject"`
	Scope         string `json:"scope"`
	Coverage      string `json:"coverage"`
	Praise        string `json:"praise"`
	Criticism     string `json:"criticism"`
	EvidenceQuote string `json:"evidence_quote"`
	Provenance    string `json:"provenance,omitempty"`
}

type spotifyResearch struct {
	Sources []spotifyCriticism `json:"sources"`
}
type researchCitation struct{ URL, Content, Title string }
type researchResponse struct {
	Content   string
	Citations []researchCitation
}
type researchSearch func(context.Context, string) (researchResponse, error)

func (r spotifyResearch) hasAlbumEvidence() bool {
	for _, s := range r.Sources {
		if s.Scope == "album" && s.Coverage == "full_album_appraisal" {
			return true
		}
	}
	return false
}
func (r spotifyResearch) render() string {
	data, _ := json.Marshal(r)
	return "Attributed published criticism (provider citations establish URL provenance, NOT independent verification of the musical claims). Repeated coverage by the same publication is one editorial voice; repeated arrangement descriptions are not independent corroboration. Preserve opposing judgments rather than averaging them into consensus:\n" + string(data)
}

func buildSpotifyGroundingQuery(typ, artist, title, year string) string {
	return fmt.Sprintf(`Research independent published music criticism of the %s %s - %s (%s). Find publication name and critic's praise or criticism with concrete musical reasons. %s Exclude artist/label promotion, announcement-only passages, store listings, biographical facts and absence-of-reviews commentary. Keep substantive independent editorial musical analysis even on a page titled as an announcement; classify the passage, not the page headline. Return ONLY JSON {"sources":[{"publication":"name","url":"exact cited URL","subject":"exact album or track title","scope":"album or single","coverage":"full_album_appraisal or track_appraisal","praise":"concrete musical praise or empty string","criticism":"concrete musical criticism or empty string","evidence_quote":"one short contiguous exact excerpt supporting the musical judgment; no stitching, paraphrase, added quotation marks or ellipses"}]}. At most 6 entries. Preserve opposing judgments separately with attribution, never invent consensus. Do not manufacture negative criticism from descriptions of distortion, abrasive timbre, chaos, disintegration or menace: these may be praised musical effects. Put a passage into criticism only if the author explicitly expresses a negative judgment; do not turn descriptive metaphors into complaints. Never relabel criticism of a different album/year as criticism of this release. At least one of praise/criticism must be substantive and contain concrete musical observations: rhythm, bass, synth timbre, melody, hooks, vocals, arrangement or musical structure. General acclaim such as top of his game, expanding the vocabulary, a new direction or release promises alone is NOT usable criticism. Summarize the actual musical details, not just the promotional verdict. The evidence excerpt must support those details. Prefer distinct editorial voices, including independent track-roundup critics; do not pad with repeated paraphrases of one page. Copy URLs from actual search citations, not memory. No usable criticism means {"sources":[]}, not an essay.`, typ, artist, title, year, researchScopeInstruction(typ))
}
func researchScopeInstruction(typ string) string {
	if typ == "album" {
		return "This pass is exclusively full-album musical appraisal, with coverage full_album_appraisal: a critic must evaluate the actual album across its music, not anticipate a release or extrapolate from a lead single. Reject album announcements, previews, premieres, promotional summaries and promises of a new direction, even when they mention the album and contain substantive single analysis. Do NOT substitute single reviews; those belong in a separate track pass."
	}
	return "This pass is exclusively criticism of the named track, with scope single."
}
func buildSpotifySinglesQuery(artist, title, year string, album *SpotifyAlbum) string {
	names := spotifyResearchTrackNames(album)
	data, _ := json.Marshal(names)
	return buildSpotifyGroundingQuery("track", artist, strings.Join(names, " / "), year) + "\nThese are candidate Spotify tracks from the album " + title + " (metadata only, do not infer sound from names): " + string(data) + ". Search critical coverage of the individual songs, including track roundups, premieres and editorial announcement pages. Singles may have been reviewed before the album year. Use one exact track name as subject per entry, scope single and coverage track_appraisal; do not evaluate the entire album."
}
func spotifyResearchTrackNames(album *SpotifyAlbum) []string {
	names := []string{}
	if album != nil {
		for _, t := range album.Tracks.Items {
			name := strings.TrimSpace(t.Name)
			if name != "" && !containsResearchName(names, name) {
				names = append(names, name)
			}
			if len(names) == 24 {
				break
			}
		}
	}
	return names
}
func containsResearchName(names []string, name string) bool {
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// URLs are never fetched. Only exact URLs supplied outside model prose in
// provider citation metadata are eligible; there is no arbitrary-fetch SSRF path.
func criticismURL(raw string) (string, error) {
	if len(raw) > 2048 {
		return "", fmt.Errorf("URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return "", fmt.Errorf("invalid source URL")
	}
	host := strings.ToLower(u.Hostname())
	if !strings.Contains(host, ".") || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return "", fmt.Errorf("local source URL")
	}
	if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return "", fmt.Errorf("private source URL")
	}
	return raw, nil
}

// This is a conservative coverage gate, NOT semantic source verification.
// Require a review signal in provider metadata or the URL path; veto preview /
// single signals in either. Some genuinely substantive pages will be excluded.
// No source URLs are fetched, so this adds no SSRF or redirect surface.
func albumCitationCoverage(rawURL, subject string, citations []researchCitation) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		return false
	}
	text := strings.ToLower(path)
	for _, c := range citations {
		if c.URL == rawURL {
			text += " " + strings.ToLower(c.Title)
		}
	}
	normalize := func(s string) string {
		return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return ' '
		}, s)), " ")
	}
	text = normalize(text)
	// Membership and a review headline alone cannot identify the release.
	if name := normalize(subject); name == "" || !strings.Contains(" "+text+" ", " "+name+" ") {
		return false
	}
	for _, marker := range []string{"single", "singles", "new track", "announces", "announcing", "announcement", "preview", "premiere", "listen to", "forthcoming", "upcoming"} {
		if strings.Contains(" "+text+" ", " "+marker+" ") {
			return false
		}
	}
	for _, word := range strings.Fields(text) {
		if word == "review" || word == "reviews" || word == "рецензия" {
			return true
		}
	}
	return false
}

func parseSpotifyResearch(resp researchResponse, scope string, subjects []string) (spotifyResearch, error) {
	var result spotifyResearch
	dec := json.NewDecoder(strings.NewReader(resp.Content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return spotifyResearch{}, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return spotifyResearch{}, fmt.Errorf("trailing research content")
	}
	if result.Sources == nil || len(result.Sources) > maxResearchSources {
		return spotifyResearch{}, fmt.Errorf("missing or excessive sources")
	}
	var fields struct {
		Sources []map[string]json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &fields); err != nil {
		return spotifyResearch{}, err
	}
	// Preserve scope conflicts even when a malformed sibling is later filtered.
	// An invalid single entry must not accidentally legitimize album use of its URL.
	singleURLs := make(map[string]bool)
	for _, entry := range result.Sources {
		if entry.Scope == "single" {
			singleURLs[entry.URL] = true
		}
	}
	invalidEntries := false
	for i, entry := range fields.Sources {
		for _, key := range []string{"publication", "url", "subject", "scope", "coverage", "praise", "criticism", "evidence_quote"} {
			value, ok := entry[key]
			if !ok || string(value) == "null" {
				result.Sources[i] = spotifyCriticism{}
				invalidEntries = true
				break
			}
		}
	}
	accepted := spotifyResearch{Sources: []spotifyCriticism{}}
	for _, s := range result.Sources {
		if s.Provenance != "" {
			invalidEntries = true
			continue
		}
		if strings.TrimSpace(s.Publication) == "" || s.Scope != scope || !containsResearchName(subjects, s.Subject) || len(strings.TrimSpace(s.EvidenceQuote)) < 16 || (len(strings.TrimSpace(s.Praise)) < 16 && len(strings.TrimSpace(s.Criticism)) < 16) {
			invalidEntries = true
			continue
		}
		if (scope == "album" && s.Coverage != "full_album_appraisal") || (scope == "single" && s.Coverage != "track_appraisal") {
			invalidEntries = true
			continue
		}
		if scope == "album" {
			if !albumCitationCoverage(s.URL, s.Subject, resp.Citations) {
				invalidEntries = true
				continue
			}
			if singleURLs[s.URL] {
				invalidEntries = true
				continue
			}
		}
		if len(s.Publication) > 200 || len(s.Subject) > 300 || len(s.Praise) > 1600 || len(s.Criticism) > 1600 || len(s.EvidenceQuote) > 500 {
			invalidEntries = true
			continue
		}
		if _, err := criticismURL(s.URL); err != nil {
			continue
		}
		for _, citation := range resp.Citations {
			if citation.URL != s.URL {
				continue
			}
			s.Provenance = "provider_citation_only; musical judgment reported by research model, not independently verified"
			if citation.Content != "" {
				// When excerpts are available, reject invented quotations rather than
				// treating URL membership as evidence that the claim appears on the page.
				if !strings.Contains(strings.Join(strings.Fields(citation.Content), " "), strings.Join(strings.Fields(s.EvidenceQuote), " ")) {
					break
				}
				s.Provenance = "provider_excerpt_contains_quote; interpretation not independently verified"
			}
			accepted.Sources = append(accepted.Sources, s)
			break
		}
	}
	if len(accepted.Sources) == 0 && invalidEntries {
		return spotifyResearch{}, fmt.Errorf("no valid criticism fields or subjects")
	}
	return accepted, nil
}

func researchSpotifyCriticism(ctx context.Context, search researchSearch, typ, artist, title, year string, album *SpotifyAlbum) spotifyResearch {
	result := spotifyResearch{Sources: []spotifyCriticism{}}
	run := func(query, scope string, subjects []string) {
		if ctx.Err() != nil {
			return
		}
		passCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		resp, err := search(passCtx, query)
		if err != nil {
			log.Printf("[spotify] Research pass failed: %v", err)
			return
		}
		parsed, err := parseSpotifyResearch(resp, scope, subjects)
		if err != nil {
			log.Printf("[spotify] Research rejected: %v", err)
			return
		}
		result.Sources = append(result.Sources, parsed.Sources...)
	}
	scope := "single"
	if typ == "album" {
		scope = "album"
	}
	run(buildSpotifyGroundingQuery(typ, artist, title, year), scope, []string{title})
	// Prioritize album criticism, then collect named-song material when needed.
	// Coverage guides research only; the writer's album verdict is independent.
	names := spotifyResearchTrackNames(album)
	if typ == "album" && !result.hasAlbumEvidence() && len(names) > 0 && ctx.Err() == nil {
		run(buildSpotifySinglesQuery(artist, title, year, album), "single", names)
	}
	return result
}

func fetchPerplexityGrounding(typ, artist, title, year string, album *SpotifyAlbum) spotifyResearch {
	if registry.Config.OpenrouterApiKey == "" {
		return spotifyResearch{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	// Decode the small provider envelope directly: the vendored SDK discards
	// citations/search_results and has no chat-message annotations field.
	client := &http.Client{Timeout: 26 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	search := func(ctx context.Context, query string) (researchResponse, error) {
		return requestSpotifyResearch(ctx, client, registry.Config.OpenrouterApiKey, query)
	}
	return researchSpotifyCriticism(ctx, search, typ, artist, title, year, album)
}

func requestSpotifyResearch(ctx context.Context, client *http.Client, key, query string) (researchResponse, error) {
	payload, err := json.Marshal(openai.ChatCompletionRequest{Model: "perplexity/sonar", Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: query}}, Temperature: 0.2, MaxTokens: 2400})
	if err != nil {
		return researchResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return researchResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return researchResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return researchResponse{}, fmt.Errorf("research HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return researchResponse{}, err
	}
	if len(body) > 1<<20 {
		return researchResponse{}, fmt.Errorf("research response too large")
	}
	var envelope struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content     string `json:"content"`
				Annotations []struct {
					Type        string `json:"type"`
					URLCitation struct {
						URL     string `json:"url"`
						Title   string `json:"title"`
						Content string `json:"content"`
					} `json:"url_citation"`
				} `json:"annotations"`
			} `json:"message"`
		} `json:"choices"`
		Citations     []string `json:"citations"`
		SearchResults []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"search_results"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return researchResponse{}, err
	}
	if len(envelope.Choices) == 0 || envelope.Choices[0].FinishReason != "stop" {
		return researchResponse{}, fmt.Errorf("empty or incomplete research completion")
	}
	result := researchResponse{Content: envelope.Choices[0].Message.Content}
	// Prefer excerpts over bare URLs for the same citation.
	for _, a := range envelope.Choices[0].Message.Annotations {
		if a.Type == "url_citation" {
			result.Citations = append(result.Citations, researchCitation{URL: a.URLCitation.URL, Content: a.URLCitation.Content, Title: a.URLCitation.Title})
		}
	}
	for _, u := range envelope.Citations {
		result.Citations = append(result.Citations, researchCitation{URL: u})
	}
	for _, s := range envelope.SearchResults {
		result.Citations = append(result.Citations, researchCitation{URL: s.URL, Title: s.Title})
	}
	return result, nil
}
