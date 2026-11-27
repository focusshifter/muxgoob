package reply

import (
	"context"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"strings"
	"testing"
)

func TestCodexDefaultComposerOwnModel(t *testing.T) {
	id := setupOrca(t)
	if err := registry.SetAiProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAiModel(id, "openai/gpt-5.4"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetImagePromptProvider(id, "openai-codex"); err != nil {
		t.Fatal(err)
	}
	for _, configured := range []string{"anthropic/composer", "orcarouter:orcarouter/auto"} {
		if err := registry.SetImagePromptModel(id, configured); err != nil {
			t.Fatal(err)
		}
		calls := interceptOrca(t, strings.TrimPrefix(configured, "orcarouter:"))
		client, model, ok := imagePromptComposer(id)
		if !ok {
			t.Fatal("missing composer")
		}
		if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
			t.Fatal(err)
		}
		if *calls != 1 {
			t.Fatalf("calls=%d", *calls)
		}
	}
}
