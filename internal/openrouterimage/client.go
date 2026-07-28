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
	"strconv"
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
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Resolution   string `json:"resolution,omitempty"`
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
	if aspectRatio, resolution, ok := sourcefulImageOptions(model, payloadRequest.Size); ok {
		// Sourceful's Image API does not accept OpenAI-style WxH in size. It
		// takes a constrained aspect ratio plus a resolution tier instead.
		payloadRequest.Size = ""
		payloadRequest.AspectRatio = aspectRatio
		payloadRequest.Resolution = resolution
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

func sourcefulImageOptions(model, size string) (aspectRatio, resolution string, ok bool) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "sourceful/riverflow-") {
		return "", "", false
	}
	width, height, parsed := parseImageSize(size)
	if !parsed {
		return "auto", "", true
	}
	aspectRatio = nearestSourcefulAspectRatio(width, height)
	largestDimension := width
	if height > largestDimension {
		largestDimension = height
	}
	switch {
	case largestDimension <= 1280:
		resolution = "1K"
	case largestDimension <= 2560:
		resolution = "2K"
	default:
		resolution = "4K"
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(model)), "-fast") && resolution == "4K" {
		resolution = "2K"
	}
	return aspectRatio, resolution, true
}

func parseImageSize(size string) (width, height int, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, widthErr := strconv.Atoi(parts[0])
	height, heightErr := strconv.Atoi(parts[1])
	if widthErr != nil || heightErr != nil || width < 1 || height < 1 {
		return 0, 0, false
	}
	return width, height, true
}

func nearestSourcefulAspectRatio(width, height int) string {
	type candidate struct {
		value  string
		width  float64
		height float64
	}
	candidates := []candidate{
		{"21:9", 21, 9}, {"16:9", 16, 9}, {"3:2", 3, 2}, {"4:3", 4, 3}, {"5:4", 5, 4},
		{"1:1", 1, 1}, {"4:5", 4, 5}, {"3:4", 3, 4}, {"2:3", 2, 3}, {"9:16", 9, 16},
	}
	target := float64(width) / float64(height)
	closest := candidates[0]
	closestDistance := abs(target - closest.width/closest.height)
	for _, candidate := range candidates[1:] {
		distance := abs(target - candidate.width/candidate.height)
		if distance < closestDistance {
			closest, closestDistance = candidate, distance
		}
	}
	return closest.value
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
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
