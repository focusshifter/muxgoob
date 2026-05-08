package openaicodex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGenerateImageViaCodexResponses(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	var got struct {
		Model string `json:"model"`
		Input []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Tools []struct {
			Type         string `json:"type"`
			Model        string `json:"model"`
			Size         string `json:"size"`
			Quality      string `json:"quality"`
			OutputFormat string `json:"output_format"`
		} `json:"tools"`
		ToolChoice struct {
			Type string `json:"type"`
		} `json:"tool_choice"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-access-token" {
			t.Fatalf("unexpected auth header: %q", auth)
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_item.done\n")
		_, _ = io.WriteString(w, `data: {"type":"response.output_item.done","item":{"type":"image_generation_call","status":"completed","revised_prompt":"a small test png","result":"`+base64.StdEncoding.EncodeToString(pngBytes)+`"}}`+"\n\n")
	}))
	defer server.Close()

	codexHome := t.TempDir()
	writeAuthFile(t, codexHome, "test-access-token")
	client := NewClient(WithBaseURL(server.URL), WithCodexHome(codexHome))
	resp, err := client.GenerateImage(context.Background(), ImageGenerationRequest{
		Prompt:       "draw a cat",
		Model:        "openai/gpt-image-2",
		Size:         "2048x2048",
		Quality:      "low",
		OutputFormat: "png",
	})
	if err != nil {
		t.Fatalf("GenerateImage error: %v", err)
	}
	if got.Model != defaultImageResponsesModel {
		t.Fatalf("expected responses model %q, got %q", defaultImageResponsesModel, got.Model)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "image_generation" || got.Tools[0].Model != "gpt-image-2" {
		t.Fatalf("unexpected image tool payload: %#v", got.Tools)
	}
	if got.Tools[0].Size != "2048x2048" || got.Tools[0].Quality != "low" || got.Tools[0].OutputFormat != "png" {
		t.Fatalf("unexpected image tool options: %#v", got.Tools[0])
	}
	if got.ToolChoice.Type != "image_generation" {
		t.Fatalf("expected forced image_generation tool choice, got %#v", got.ToolChoice)
	}
	if string(resp.Image) != string(pngBytes) {
		t.Fatalf("unexpected image bytes: %#v", resp.Image)
	}
	if resp.Model != "gpt-image-2" || resp.MimeType != "image/png" || resp.Extension != "png" || resp.RevisedPrompt != "a small test png" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestImageHTTPClientExtendsShortTimeoutForSlowImageGenerations(t *testing.T) {
	shortTimeout := defaultImageGenerationTimeout / 5
	base := &http.Client{Timeout: shortTimeout}
	got := imageHTTPClient(base)
	if got == base {
		t.Fatal("expected short-timeout client to be cloned")
	}
	if got.Timeout != defaultImageGenerationTimeout {
		t.Fatalf("expected image timeout %v, got %v", defaultImageGenerationTimeout, got.Timeout)
	}
	if base.Timeout != shortTimeout {
		t.Fatalf("base client timeout was mutated: %v", base.Timeout)
	}
}
