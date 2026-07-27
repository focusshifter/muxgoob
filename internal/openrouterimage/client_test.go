package openrouterimage

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
)

func TestClientGenerateImage(t *testing.T) {
	image := []byte("generated-webp")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/images" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer test-key" {
			t.Fatalf("authorization = %q", authorization)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		for _, expected := range []string{`"model":"black-forest-labs/flux.2-pro"`, `"prompt":"a silver Ferrari"`, `"size":"1024x1024"`, `"n":1`} {
			if !strings.Contains(string(body), expected) {
				t.Fatalf("request missing %s: %s", expected, body)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(image) + `","media_type":"image/webp"}]}`))
	}))
	defer server.Close()

	client := NewClient("test-key", WithBaseURL(server.URL))
	response, err := client.GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{
		Prompt:       "a silver Ferrari",
		Model:        "openrouter/black-forest-labs/flux.2-pro",
		Size:         "1024x1024",
		Quality:      "low",
		OutputFormat: "webp",
	})
	if err != nil {
		t.Fatalf("GenerateImage returned error: %v", err)
	}
	if response.Model != "black-forest-labs/flux.2-pro" || response.MimeType != "image/webp" || response.Extension != "webp" || string(response.Image) != string(image) {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestClientGenerateImageRejectsEmptyData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, err := NewClient("test-key", WithBaseURL(server.URL)).GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Prompt: "cat", Model: "google/gemini-3.1-flash-image"})
	if err == nil || !strings.Contains(err.Error(), "contains no image") {
		t.Fatalf("expected missing image error, got %v", err)
	}
}
