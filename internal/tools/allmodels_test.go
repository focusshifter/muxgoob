package tools

import (
	"context"
	"encoding/json"
	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/focusshifter/muxgoob/utils/testutils"
	openai "github.com/sashabaranov/go-openai"
	"io"
	"net/http"
	"strings"
	"testing"
)

type allmodelsTransport func(*http.Request) (*http.Response, error)

func (f allmodelsTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func setupAllModels(t *testing.T) *int64 {
	t.Helper()
	oldDB, oldConfig, oldTransport := database.DB, registry.Config, http.DefaultTransport
	db := testutils.SetupTestDB(t)
	database.DB = db
	t.Cleanup(func() {
		db.Close()
		database.DB = oldDB
		registry.Config = oldConfig
		http.DefaultTransport = oldTransport
	})
	if err := registry.EnsurePluginSettingsTable(); err != nil {
		t.Fatal(err)
	}
	registry.Config.AiProvider = "openrouter"
	registry.Config.AiModel = "wrong-global"
	registry.Config.OpenrouterApiKey = "wrong-openrouter-key"
	registry.Config.AllmodelsApiKey = "allmodels-test"
	chatID := int64(92834)
	if err := registry.SetAiProvider(&chatID, "allmodels"); err != nil {
		t.Fatal(err)
	}
	return &chatID
}
func interceptAllModels(t *testing.T, model string) *int {
	t.Helper()
	calls := new(int)
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if r.URL.Host != "api.allmodels.io" || r.URL.Path != "/oai/chat/completions" || r.Header.Get("Authorization") != "Bearer allmodels-test" {
			t.Fatalf("wrong route/auth %s %q", r.URL, r.Header.Get("Authorization"))
		}
		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != model {
			t.Fatalf("model=%q want=%q", req.Model, model)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"fixture reply"}}]}`))}, nil
	})
	return calls
}

func TestAllModelsChatTopicsRouting(t *testing.T) {
	id := setupAllModels(t)
	registry.SetAiModel(id, "deepseek/deepseek-v4.1-flash-abliterated")
	calls := interceptAllModels(t, "deepseek/deepseek-v4.1-flash-abliterated")
	client, model := buildLightweightClient(*id)
	if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: model}); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("calls=%d", *calls)
	}
}

func TestAllModelsPrefixedTopicsAndImageGenerationRejection(t *testing.T) {
	id := setupAllModels(t)
	registry.SetAiProvider(id, "openai-codex")
	const model = "deepseek/deepseek-v4.1-flash-abliterated"
	registry.SetAiModel(id, "allmodels:"+model)
	calls := interceptAllModels(t, model)
	client, actual := buildLightweightClient(*id)
	if _, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: actual}); err != nil {
		t.Fatal(err)
	}
	registry.SetImageAiModel(id, "allmodels:"+model)
	if _, err := imageGeneratorForChat(*id).GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{}); err == nil {
		t.Fatal("unsupported image route fell through")
	}
	if *calls != 1 {
		t.Fatal("unexpected call")
	}
}
