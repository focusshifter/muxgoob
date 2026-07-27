// Package openrouterimage implements OpenRouter's Image API.
package openrouterimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

type Option func(*Client)

type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewClient(apiKey string, options ...Option) *Client {
	client := &Client{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}
	return client
}

func WithBaseURL(baseURL string) Option {
	return func(client *Client) {
		if value := strings.TrimRight(strings.TrimSpace(baseURL), "/"); value != "" {
			client.baseURL = value
		}
	}
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

type imageRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	N            int    `json:"n"`
	Provider     struct {
		AllowFallbacks bool `json:"allow_fallbacks"`
	} `json:"provider"`
}

type imageResponse struct {
	Data []struct {
		Base64    string `json:"b64_json"`
		MediaType string `json:"media_type"`
	} `json:"data"`
}

func (c *Client) GenerateImage(ctx context.Context, request openaicodex.ImageGenerationRequest) (openaicodex.ImageGenerationResponse, error) {
	if c == nil || c.apiKey == "" {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("openrouter API key is not configured")
	}
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("prompt is required")
	}
	model := strings.TrimPrefix(strings.TrimSpace(request.Model), "openrouter/")
	if model == "" {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("OpenRouter image model is not configured")
	}
	payloadRequest := imageRequest{
		Model:        model,
		Prompt:       prompt,
		Size:         strings.TrimSpace(request.Size),
		Quality:      strings.TrimSpace(request.Quality),
		OutputFormat: strings.TrimSpace(request.OutputFormat),
		N:            1,
	}
	// A configured OpenRouter image model is an explicit choice. Do not silently
	// route a failed image generation through another upstream provider.
	payloadRequest.Provider.AllowFallbacks = false
	payload, err := json.Marshal(payloadRequest)
	if err != nil {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("marshal OpenRouter image request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/images", bytes.NewReader(payload))
	if err != nil {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("build OpenRouter image request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("send OpenRouter image request: %w", err)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("read OpenRouter image response: %w", err)
	}
	if httpResponse.StatusCode >= http.StatusBadRequest {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("OpenRouter image error: status=%d body=%s", httpResponse.StatusCode, strings.TrimSpace(string(body)))
	}
	var response imageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("decode OpenRouter image response: %w", err)
	}
	if len(response.Data) == 0 || strings.TrimSpace(response.Data[0].Base64) == "" {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("OpenRouter image response contains no image")
	}
	image, err := base64.StdEncoding.DecodeString(response.Data[0].Base64)
	if err != nil {
		return openaicodex.ImageGenerationResponse{}, fmt.Errorf("decode OpenRouter image data: %w", err)
	}
	mimeType := strings.TrimSpace(response.Data[0].MediaType)
	if mimeType == "" {
		mimeType = "image/png"
	}
	return openaicodex.ImageGenerationResponse{
		Model:     model,
		Image:     image,
		MimeType:  mimeType,
		Extension: extensionForMIMEType(mimeType),
	}, nil
}

func extensionForMIMEType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/svg+xml":
		return "svg"
	default:
		return "png"
	}
}
