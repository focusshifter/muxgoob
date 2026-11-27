package orcarouterimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
)

func imageBytes(t *testing.T, format string) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&out, img, nil)
	} else {
		err = png.Encode(&out, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestGenerateImageBase64(t *testing.T) {
	data := imageBytes(t, "png")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" || r.Method != http.MethodPost {
			t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer orca-test" {
			t.Error("wrong auth")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for k, want := range map[string]any{"model": "orcarouter/openai/gpt-image-1", "prompt": "a cat", "n": float64(1), "size": "1024x1024", "quality": "low", "response_format": "b64_json"} {
			if body[k] != want {
				t.Errorf("%s = %v; want %v", k, body[k], want)
			}
		}
		if _, ok := body["provider"]; ok {
			t.Error("OpenRouter routing must not leak into Orca payload")
		}
		if _, ok := body["output_format"]; ok {
			t.Error("undocumented output_format sent")
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q,"revised_prompt":"a revised cat"}]}`, base64.StdEncoding.EncodeToString(data))
	}))
	defer server.Close()
	client := NewClient(" orca-test ", WithBaseURL(server.URL+"/v1/"))
	got, err := client.GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Model: "orcarouter/openai/gpt-image-1", Prompt: " a cat ", Size: "1024x1024", Quality: "low", OutputFormat: "png"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Image, data) || got.MimeType != "image/png" || got.Extension != "png" || got.Model != "orcarouter/openai/gpt-image-1" || got.RevisedPrompt != "a revised cat" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestGenerateImageURL(t *testing.T) {
	data := imageBytes(t, "jpeg")
	downloads := 0
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credentials leaked to download")
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(data)
	}))
	defer imageServer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"url":%q}]}`, imageServer.URL+"/image")
	}))
	defer server.Close()
	got, err := NewClient("orca-test", WithBaseURL(server.URL)).GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Model: "grok/grok-imagine-image", Prompt: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	if downloads != 1 || !bytes.Equal(got.Image, data) || got.Extension != "jpg" || got.MimeType != "image/jpeg" {
		t.Fatalf("unexpected image: %+v", got)
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected network request") }))
	defer server.Close()
	for _, tc := range []struct{ key, model, prompt, format, want string }{
		{" ", "openai/gpt-image-1", "cat", "", "API key"},
		{"key", "", "cat", "", "model"},
		{"key", "openai/gpt-image-1", " ", "", "prompt"},
		{"key", "google/gemini-3.1-flash-image-preview", "cat", "", "chat-completions"},
		{"key", "openai/gpt-image-1", "cat", "webp", "output_format"},
	} {
		_, err := NewClient(tc.key, WithBaseURL(server.URL)).GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Model: tc.model, Prompt: tc.prompt, OutputFormat: tc.format})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("want %q, got %v", tc.want, err)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"empty", `{"data":[]}`, "no image", 200},
		{"invalid json", `{`, "decode OrcaRouter", 200},
		{"invalid base64", `{"data":[{"b64_json":"!!!"}]}`, "decode OrcaRouter image data", 200},
		{"not image", `{"data":[{"b64_json":"aGVsbG8="}]}`, "unsupported image content", 200},
		{"bad URL", `{"data":[{"url":"file:///etc/passwd"}]}`, "image URL", 200},
		{"error", `{"error":"orca-secret denied"}`, "status=401", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			_, err := NewClient("orca-secret", WithBaseURL(server.URL)).GenerateImage(context.Background(), openaicodex.ImageGenerationRequest{Model: "openai/gpt-image-1", Prompt: "cat"})
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "orca-secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBoundedRead(t *testing.T) {
	if _, err := readBounded(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("oversize body accepted")
	}
	if got, err := readBounded(strings.NewReader("1234"), 4); err != nil || string(got) != "1234" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTimeoutAndNoRedirect(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("redirect followed") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	req := openaicodex.ImageGenerationRequest{Model: "openai/gpt-image-1", Prompt: "cat"}
	_, err := NewClient("key", WithBaseURL(redirect.URL)).GenerateImage(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "status=307") || calls != 0 {
		t.Fatalf("redirect result: %v, %d", err, calls)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"url":%q}]}`, redirect.URL)
	}))
	defer api.Close()
	_, err = NewClient("key", WithBaseURL(api.URL)).GenerateImage(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "download error: status=307") || calls != 0 {
		t.Fatalf("download redirect result: %v, %d", err, calls)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer server.Close()
	_, err = NewClient("key", WithBaseURL(server.URL), WithHTTPClient(&http.Client{Timeout: 20 * time.Millisecond})).GenerateImage(context.Background(), req)
	if err == nil {
		t.Fatal("expected timeout")
	}
}
