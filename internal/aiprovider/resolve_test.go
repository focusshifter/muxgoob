package aiprovider

import "testing"

func TestResolveModelRoute(t *testing.T) {
	for _, tc := range []struct {
		model, provider, want string
		bad                   bool
	}{
		{"anthropic/claude-opus-5.5", OrcaRouter, "anthropic/claude-opus-5.5", false},
		{"google/gemini:online", OrcaRouter, "google/gemini:online", false},
		{"openai/gpt-6.1-sol", "openai-codex", "openai/gpt-6.1-sol", false},
		{"gpt-5.4", "openai-codex", "gpt-5.4", false},
		{"orcarouter:google/gemini", OrcaRouter, "google/gemini", false},
		{"orcarouter:orcarouter/auto", OrcaRouter, "orcarouter/auto", false},
		{"orcarouter:orcarouter/free", OrcaRouter, "orcarouter/free", false},
		{"orcarouter/auto", OrcaRouter, "orcarouter/auto", false},
		{"openrouter:vendor/model:online", "openrouter", "vendor/model:online", false},
		{"orcarouter:openrouter:vendor/model", OrcaRouter, "openrouter:vendor/model", false},
		{"unknown:vendor/model", OrcaRouter, "unknown:vendor/model", false},
		{"orcarouter:", "", "", true}, {"openrouter:   ", "", "", true},
	} {
		t.Run(tc.model, func(t *testing.T) {
			p, m, err := Resolve("openai-codex", tc.model)
			if (err != nil) != tc.bad || (!tc.bad && (p != tc.provider || m != tc.want)) {
				t.Fatalf("got %q %q %v", p, m, err)
			}
		})
	}
}
