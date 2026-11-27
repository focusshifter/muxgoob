// Package orcarouterimage implements OrcaRouter's dedicated Images API.
// Gemini image models require Chat Completions and are deliberately unsupported.
package orcarouterimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
)

const (
	defaultBaseURL   = "https://api.orcarouter.ai/v1"
	maxResponseBytes = 64 * 1024 * 1024
	maxImageBytes    = 48 * 1024 * 1024
	maxErrorBytes    = 4096
)

type Option func(*Client)

type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewClient(apiKey string, options ...Option) *Client {
	c := &Client{apiKey: strings.TrimSpace(apiKey), baseURL: defaultBaseURL, httpClient: &http.Client{Timeout: 10 * time.Minute}}
	for _, option := range options {
		if option != nil {
			option(c)
		}
	}
	return c
}

func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		if value := strings.TrimRight(strings.TrimSpace(baseURL), "/"); value != "" {
			c.baseURL = value
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

type imageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format"`
}

type imageResponse struct {
	Data []struct {
		Base64        string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
}

// GeneratedError means the paid generation succeeded, but retrieving or preparing
// its result failed. Callers must never retry the generation POST.
type GeneratedError struct{ Err error }

func (e *GeneratedError) Error() string {
	return "image generation succeeded but retrieval failed (do not regenerate): " + e.Err.Error()
}
func (e *GeneratedError) Unwrap() error                  { return e.Err }
func (e *GeneratedError) ImageGenerationCompleted() bool { return true }

func (c *Client) GenerateImage(ctx context.Context, request openaicodex.ImageGenerationRequest) (_ openaicodex.ImageGenerationResponse, returnedErr error) {
	var zero openaicodex.ImageGenerationResponse
	if c == nil || c.apiKey == "" {
		return zero, fmt.Errorf("OrcaRouter API key is not configured")
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return zero, fmt.Errorf("prompt is required")
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		return zero, fmt.Errorf("OrcaRouter image model is not configured")
	}
	if strings.Contains(strings.ToLower(model), "gemini") {
		return zero, fmt.Errorf("OrcaRouter Gemini chat-completions image generation is not supported; configure a dedicated Images API model")
	}
	// The dedicated API documents response_format, not output_format. PNG is
	// the tool's default; returned bytes, not that preference, determine the file.
	format := strings.ToLower(strings.TrimSpace(request.OutputFormat))
	if format != "" && format != "png" {
		return zero, fmt.Errorf("OrcaRouter Images API output_format %q is unsupported; use the default provider format", format)
	}
	payload, err := json.Marshal(imageRequest{Model: model, Prompt: prompt, N: 1, Size: strings.TrimSpace(request.Size), Quality: strings.TrimSpace(request.Quality), ResponseFormat: "b64_json"})
	if err != nil {
		return zero, fmt.Errorf("marshal OrcaRouter image request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images/generations", bytes.NewReader(payload))
	if err != nil {
		return zero, fmt.Errorf("build OrcaRouter image request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	response, err := c.do(req)
	if err != nil {
		return zero, fmt.Errorf("send OrcaRouter image request: %s", c.redact(err.Error()))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return zero, c.responseError(response)
	}
	// Any failure after a successful API response is a retrieval/preparation
	// failure, never permission to issue another billed generation request.
	defer func() {
		if returnedErr != nil {
			returnedErr = &GeneratedError{Err: returnedErr}
		}
	}()
	body, err := readBounded(response.Body, maxResponseBytes)
	if err != nil {
		return zero, fmt.Errorf("read OrcaRouter image response: %w", err)
	}
	var result imageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return zero, fmt.Errorf("decode OrcaRouter image response: %w", err)
	}
	if len(result.Data) == 0 {
		return zero, fmt.Errorf("OrcaRouter image response contains no image")
	}
	first := result.Data[0]
	var data []byte
	if encoded := strings.TrimSpace(first.Base64); encoded != "" {
		if base64.StdEncoding.DecodedLen(len(encoded)) > maxImageBytes {
			return zero, fmt.Errorf("OrcaRouter image exceeds size limit")
		}
		data, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return zero, fmt.Errorf("decode OrcaRouter image data: %w", err)
		}
	} else if strings.TrimSpace(first.URL) != "" {
		// Retain the exact signed URL locally and retry its download once only;
		// never repeat the generation POST or disclose the URL in errors/logs.
		imageURL := strings.TrimSpace(first.URL)
		data, err = c.download(ctx, imageURL)
		if err != nil && ctx.Err() == nil {
			data, err = c.download(ctx, imageURL)
		}
		if err != nil {
			return zero, err
		}
	} else {
		return zero, fmt.Errorf("OrcaRouter image response contains no image")
	}
	if len(data) == 0 {
		return zero, fmt.Errorf("OrcaRouter image response contains no image")
	}
	mimeType := http.DetectContentType(data)
	var extension string
	switch mimeType {
	case "image/png":
		extension = "png"
	case "image/jpeg":
		extension = "jpg"
	case "image/webp":
		extension = "webp"
	case "image/gif":
		extension = "gif"
	default:
		return zero, fmt.Errorf("OrcaRouter returned unsupported image content type %q", mimeType)
	}
	return openaicodex.ImageGenerationResponse{Model: model, Image: data, MimeType: mimeType, Extension: extension, RevisedPrompt: first.RevisedPrompt}, nil
}

// Do not follow redirects on authenticated API requests or untrusted image URLs.
// Use a copy to avoid mutating a shared client. A download never uses a cookie
// jar or an Authorization header, even when hosted on the API's own origin.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	if client.Timeout == 0 {
		client.Timeout = 10 * time.Minute
	}
	return client.Do(req)
}

func (c *Client) download(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("OrcaRouter returned an invalid image URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build OrcaRouter image download request")
	}
	response, err := c.do(req)
	// Avoid exposing signed image URLs (or credentials echoed by a server).
	if err != nil {
		return nil, fmt.Errorf("OrcaRouter image download failed: %s", c.redactTransportError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OrcaRouter image download error: status=%d", response.StatusCode)
	}
	data, err := readBounded(response.Body, maxImageBytes)
	if err != nil {
		return nil, fmt.Errorf("read OrcaRouter image download: %w", err)
	}
	return data, nil
}

func (c *Client) redactTransportError(err error) string {
	if e, ok := err.(*url.Error); ok {
		return c.redact(e.Err.Error())
	}
	return c.redact(err.Error())
}

func (c *Client) responseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBytes))
	return fmt.Errorf("OrcaRouter image error: status=%d body=%s", response.StatusCode, c.redact(strings.TrimSpace(string(body))))
}

func (c *Client) redact(text string) string { return strings.ReplaceAll(text, c.apiKey, "[REDACTED]") }

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("body exceeds size limit (%d bytes)", limit)
	}
	return data, nil
}
