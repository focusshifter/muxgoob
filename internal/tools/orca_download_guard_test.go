package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/internal/orcarouterimage"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

// Exercise the real client -> retry helper -> repeated tool-call boundary.
func TestOrcaDownloadFailureNeverRegenerates(t *testing.T) {
	for _, failure := range []string{"timeout", "http403", "badbytes"} {
		t.Run(failure, func(t *testing.T) {
			id := setupOrca(t)
			if err := registry.SetImageAiModel(id, "orcarouter:orcarouter/opaque-image"); err != nil {
				t.Fatal(err)
			}
			posts, gets := 0, 0
			http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
				body := ""
				status := 200
				if r.Method == http.MethodPost {
					posts++
					var req struct{ Model string }
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					if req.Model != "orcarouter/opaque-image" {
						t.Fatalf("model rewritten: %q", req.Model)
					}
					if r.URL.Host != "api.orcarouter.ai" {
						t.Fatal(r.URL.Host)
					}
					body = `{"data":[{"url":"https://images.invalid/result?secret=signed-url"}]}`
				} else {
					gets++
					if r.Header.Get("Authorization") != "" {
						t.Fatal("download received credentials")
					}
					switch failure {
					case "timeout":
						return nil, fmt.Errorf("fixture timeout")
					case "http403":
						status = 403
					case "badbytes":
						body = "not an image"
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			tool := NewGenerateImageTool(*id)
			tool.notify = func(int64, telebot.ChatAction) error { return nil }
			for i := 0; i < 2; i++ {
				_, err := tool.Execute(context.Background(), `{"prompt":"cat"}`)
				if err == nil {
					t.Fatal("download failure accepted")
				}
				if strings.Contains(err.Error(), "signed-url") {
					t.Fatal("signed URL disclosed")
				}
			}
			if posts != 1 || gets < 1 || gets > 2 || !tool.generationCompleted {
				t.Fatalf("posts=%d gets=%d completed=%t", posts, gets, tool.generationCompleted)
			}
		})
	}
}

func TestOrcaImageEmptyRouteBeforeNetwork(t *testing.T) {
	id := setupOrca(t)
	if err := registry.SetImageAiModel(id, "orcarouter:"); err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = orcaTransport(func(*http.Request) (*http.Response, error) { t.Fatal("empty route reached network"); return nil, nil })
	_, err := NewGenerateImageTool(*id).Execute(context.Background(), `{"prompt":"cat"}`)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err=%v", err)
	}
}

func TestOrcaDownloadRetryUsesOnlyOriginalURL(t *testing.T) {
	setupOrca(t)
	posts, gets := 0, 0
	http.DefaultTransport = orcaTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		status := 200
		if r.Method == "POST" {
			posts++
			body = `{"data":[{"url":"https://images.invalid/signed"}]}`
		} else {
			gets++
			if r.URL.String() != "https://images.invalid/signed" {
				t.Fatal(r.URL)
			}
			if gets == 1 {
				return nil, fmt.Errorf("timeout")
			}
			body = "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	result, err := orcarouterimage.NewClient("fixture").GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Model: "vendor/image", Prompt: "cat"})
	if err != nil || len(result.Image) == 0 || posts != 1 || gets != 2 {
		t.Fatalf("posts=%d gets=%d err=%v", posts, gets, err)
	}
}
