package spotify

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
)

// Opt-in provider smoke test; never publishes or writes the production DB.
func TestSpotifyResearchLive(t *testing.T) {
	key := os.Getenv("GOOBY_RESEARCH_SMOKE_KEY")
	if key == "" {
		t.Skip("GOOBY_RESEARCH_SMOKE_KEY not set")
	}
	album := &SpotifyAlbum{}
	if err := json.Unmarshal([]byte(`{"tracks":{"items":[{"name":"Sound of the Void"},{"name":"Without"},{"name":"Mortal"},{"name":"Subject"},{"name":"Chances"},{"name":"Dimension"},{"name":"Forever"},{"name":"Abandon"},{"name":"Returning"},{"name":"Phantom"}]}}`), album); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 26 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	research := researchSpotifyCriticism(ctx, func(ctx context.Context, query string) (researchResponse, error) {
		response, err := requestSpotifyResearch(ctx, client, key, query)
		if err == nil {
			t.Logf("provider citations=%d; content=%s", len(response.Citations), response.Content)
			for _, citation := range response.Citations {
				if citation.Title != "" {
					t.Logf("provider metadata url=%s title=%s", citation.URL, citation.Title)
				}
			}
		}
		return response, err
	}, "album", "Kontravoid", "Sound of the Void", "2026", album)
	body, err := json.MarshalIndent(research, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("accepted research: %s", body)
	if len(research.Sources) == 0 {
		t.Fatal("live research found no accepted criticism")
	}
	if os.Getenv("GOOBY_REVIEW_SMOKE_MODEL") == "" {
		return
	}
	oldConfig := registry.Config
	defer func() { registry.Config = oldConfig }()
	registry.Config.SpotifyReviewPrompt = os.Getenv("GOOBY_REVIEW_SMOKE_PROMPT")
	prompt := buildSpotifyReviewPrompt("album", "Kontravoid", "Sound of the Void", "2026", spotifyReviewGrounding(research.render(), album))
	info := openaicodex.NormalizeConfiguredModel(os.Getenv("GOOBY_REVIEW_SMOKE_MODEL"))
	if !info.UseCodex {
		t.Fatal("smoke model must use Codex subscription route")
	}
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer finalCancel()
	response, err := openaicodex.NewClient().CreateChatCompletion(finalCtx, buildSpotifyReviewCompletionRequest(info.RawModel, prompt, "album"))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) == 0 {
		t.Fatal("empty final review")
	}
	review, rating, err := parseSpotifyReviewCompletion(response.Choices[0].Message.Content, "album")
	if err != nil {
		t.Fatal(err)
	}
	if !rating.Valid || !isValidAlbumRating(rating.Float64) {
		t.Fatal("album draft must have a score regardless of research scope")
	}
	t.Logf("review draft (rating valid=%v):\n%s", rating.Valid, review)
	if out := os.Getenv("GOOBY_REVIEW_SMOKE_OUTPUT"); out != "" {
		if err := os.WriteFile(out, []byte(review), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
