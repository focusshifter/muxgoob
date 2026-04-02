package spotify

import (
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/registry"
)

func TestBuildSpotifyReviewPrompt_DefaultFallback(t *testing.T) {
	registry.Config = registry.Configuration{}

	prompt := buildSpotifyReviewPrompt("album", "World's End Girlfriend", "Helix of Frequency", "2025", "mixed reception")

	checks := []string{
		"Semi-follow the overall consensus",
		"Do not be automatically harsh",
		"Mention both what works and what does not",
		"mixed reception",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}

func TestBuildSpotifyReviewPrompt_CustomTemplate(t *testing.T) {
	registry.Config = registry.Configuration{
		SpotifyReviewPrompt: "Review {artist} - {title} ({year}) as a {type}. Facts: {grounding}",
	}

	prompt := buildSpotifyReviewPrompt("album", "Deer Hunter", "Sunya", "2025", "praised groove, criticized repetition")

	checks := []string{
		"Review Deer Hunter - Sunya (2025) as a album.",
		"praised groove, criticized repetition",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Fatalf("expected prompt to contain %q, got %q", check, prompt)
		}
	}
}
