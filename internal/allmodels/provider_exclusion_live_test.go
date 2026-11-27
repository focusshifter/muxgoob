package allmodels

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// Provider-exclusion wiring smoke only: it does NOT reproduce a natural empty
// or invoke the automatic retry. Explicit opt-in; at most two paid POSTs total.
func TestAllModelsLiveProviderExclusion(t *testing.T) {
	if os.Getenv("ALLMODELS_LIVE_TEST") != "1" {
		t.Skip("requires explicit ALLMODELS_LIVE_TEST=1")
	}
	key := os.Getenv("ALLMODELS_API_KEY")
	if strings.TrimSpace(key) == "" {
		t.Fatal("ALLMODELS_API_KEY is required")
	}
	model := os.Getenv("ALLMODELS_TEST_MODEL")
	if model == "" || strings.TrimSpace(model) != model || strings.HasPrefix(model, "allmodels:") || strings.HasSuffix(model, ":online") || model == "online:auto" {
		t.Fatal("ALLMODELS_TEST_MODEL must be an explicit exact plain model ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := NewClient(key)
	body := map[string]any{"model": model, "max_tokens": 2048, "messages": []map[string]string{{"role": "user", "content": "Reply exactly ROUTE_OK"}}, "provider": map[string]any{"zdr": true}}
	first := ""
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt == 2 {
			body["provider"] = map[string]any{"zdr": true, "ignore": []string{first}}
		}
		data, err := c.exchange(ctx, body)
		if err != nil {
			t.Fatalf("provider-exclusion smoke attempt %d failed: %v", attempt, err)
		}
		var out openai.ChatCompletionResponse
		if json.Unmarshal(data, &out) != nil {
			t.Fatal("provider-exclusion smoke response decode failed")
		}
		provider := c.observedProvider(data)
		if provider == "" {
			t.Fatal("provider-exclusion smoke omitted a usable observed provider")
		}
		if len(out.Choices) != 1 || out.Choices[0].FinishReason != openai.FinishReasonStop || strings.TrimSpace(out.Choices[0].Message.Content) != "ROUTE_OK" {
			t.Fatal("provider-exclusion smoke did not return the neutral marker")
		}
		if attempt == 1 {
			first = provider
		} else if strings.EqualFold(first, provider) {
			t.Fatal("provider-exclusion smoke returned the excluded upstream")
		}
		t.Logf("provider-exclusion smoke attempt=%d id=%s provider=%s text_bytes=%d zdr=true", attempt, diagnosticLabel(out.ID, key), diagnosticLabel(provider, key), len(out.Choices[0].Message.Content))
	}
}
