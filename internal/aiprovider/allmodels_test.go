package aiprovider

import (
	"context"
	"github.com/focusshifter/muxgoob/registry"
	openai "github.com/sashabaranov/go-openai"
	"net/http"
	"testing"
)

func TestAllModelsExplicitOnlyResolve(t *testing.T) {
	const model = "deepseek/deepseek-v4.1-flash-abliterated"
	for _, tc := range []struct{ provider, configured, wantProvider, wantModel string }{
		{"openai-codex", "allmodels:" + model, "allmodels", model},
		{"openrouter", "allmodels:" + model + ":online", "allmodels", model + ":online"},
		{"allmodels", model, "allmodels", model},
		{"allmodels", "allmodels/auto", "allmodels", "allmodels/auto"},
		{"openai-codex", model, "orcarouter", model},
		{"openai-codex", "openai/gpt-5.4", "openai-codex", "openai/gpt-5.4"},
		{"openai-codex", "orcarouter/auto", "orcarouter", "orcarouter/auto"},
		{"openai-codex", "allmodels:openrouter:vendor/model", "allmodels", "openrouter:vendor/model"},
	} {
		p, m, err := Resolve(tc.provider, tc.configured)
		if err != nil || p != tc.wantProvider || m != tc.wantModel {
			t.Fatalf("%+v -> %s/%s %v", tc, p, m, err)
		}
	}
	if _, _, err := Resolve("openai-codex", "allmodels: "); err == nil {
		t.Fatal("empty routed ID accepted")
	}
	if !IsRouter(AllModels) || !IsChatProvider(AllModels) || IsImageProvider(AllModels) {
		t.Fatal("incorrect AllModels capability registration")
	}
}

func TestAllModelsKeyIsolation(t *testing.T) {
	oldConfig, oldTransport := registry.Config, http.DefaultTransport
	t.Cleanup(func() { registry.Config = oldConfig; http.DefaultTransport = oldTransport })
	registry.Config.AllmodelsApiKey = ""
	registry.Config.OrcarouterApiKey = "wrong-orca"
	registry.Config.OpenrouterApiKey = "wrong-openrouter"
	calls := 0
	http.DefaultTransport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		t.Fatal("missing key reached network")
		return nil, nil
	})
	_, err := Router(AllModels).CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{Model: "vendor/model:online"})
	if err == nil || calls != 0 {
		t.Fatal("missing key accepted")
	}
}
