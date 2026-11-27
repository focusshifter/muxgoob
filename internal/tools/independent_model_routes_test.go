package tools

import (
	"context"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"strings"
	"testing"
)

func TestCodexDefaultTopicsModel(t *testing.T) {
	id := setupOrca(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	for _, configured := range []string{"google/topics", "orcarouter:orcarouter/auto", "openrouter:vendor/topic"} {
		if err := registry.SetAiModel(id, configured); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(configured, "openrouter:") {
			continue
		}
		calls := interceptOrca(t, strings.TrimPrefix(configured, "orcarouter:"))
		client, model := buildLightweightClient(*id)
		if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
			t.Fatal(err)
		}
		if *calls != 1 {
			t.Fatalf("calls=%d", *calls)
		}
	}
}
