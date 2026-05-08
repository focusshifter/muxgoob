package openaicodex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

const (
	defaultImageResponsesModel = "gpt-5.5"
	defaultImageModel          = "gpt-image-2"
	defaultImageInstructions   = "You are an image generation assistant."
	maxImageResultBase64Chars  = 64 * 1024 * 1024
)

type ImageGenerationRequest struct {
	Prompt       string
	Model        string
	Size         string
	Quality      string
	OutputFormat string
}

type ImageGenerationResponse struct {
	Model         string
	Image         []byte
	MimeType      string
	Extension     string
	RevisedPrompt string
}

type codexImageTool struct {
	Type         string `json:"type"`
	Model        string `json:"model"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

type codexImageToolChoice struct {
	Type string `json:"type"`
}

type codexImageRequest struct {
	Model        string               `json:"model"`
	Instructions string               `json:"instructions"`
	Store        bool                 `json:"store"`
	Stream       bool                 `json:"stream"`
	Input        []codexMessageInput  `json:"input"`
	Tools        []codexImageTool     `json:"tools"`
	ToolChoice   codexImageToolChoice `json:"tool_choice"`
}

type codexImageEvent struct {
	Type string `json:"type"`
	Item struct {
		Type          string `json:"type"`
		Status        string `json:"status"`
		Result        string `json:"result"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"item"`
	Response struct {
		Output []struct {
			Type          string `json:"type"`
			Result        string `json:"result"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"output"`
	} `json:"response"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
}

func (c *Client) GenerateImage(ctx context.Context, request ImageGenerationRequest) (ImageGenerationResponse, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return ImageGenerationResponse{}, fmt.Errorf("prompt is required")
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = defaultImageModel
	}
	model = strings.TrimPrefix(model, "openai/")
	size := strings.TrimSpace(request.Size)
	if size == "" {
		size = "1024x1024"
	}
	outputFormat := strings.TrimSpace(request.OutputFormat)
	if outputFormat == "" {
		outputFormat = "png"
	}

	accessToken, err := c.loadAccessToken()
	if err != nil {
		return ImageGenerationResponse{}, err
	}

	payload := codexImageRequest{
		Model:        defaultImageResponsesModel,
		Instructions: defaultImageInstructions,
		Store:        false,
		Stream:       true,
		Input: []codexMessageInput{{
			Role: "user",
			Content: []codexTextPart{{
				Type: "input_text",
				Text: prompt,
			}},
		}},
		Tools: []codexImageTool{{
			Type:         "image_generation",
			Model:        model,
			Size:         size,
			Quality:      strings.TrimSpace(request.Quality),
			OutputFormat: outputFormat,
		}},
		ToolChoice: codexImageToolChoice{Type: "image_generation"},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return ImageGenerationResponse{}, fmt.Errorf("marshal codex image request: %w", err)
	}
	url := strings.TrimRight(c.baseURL, "/") + "/responses"
	log.Printf("[openaicodex] image request responses_model=%s image_model=%s size=%s quality=%s output_format=%s prompt_len=%d", payload.Model, model, size, request.Quality, outputFormat, len(prompt))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ImageGenerationResponse{}, fmt.Errorf("build codex image request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ImageGenerationResponse{}, fmt.Errorf("send codex image request: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(httpResp.Body)
		return ImageGenerationResponse{}, fmt.Errorf("codex image responses error: status=%d body=%s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	result, err := parseImageSSE(httpResp.Body, outputFormat)
	if err != nil {
		return ImageGenerationResponse{}, err
	}
	result.Model = model
	return result, nil
}

func parseImageSSE(body io.Reader, outputFormat string) (ImageGenerationResponse, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxImageResultBase64Chars+1024*1024)
	var last codexImageEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event codexImageEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return ImageGenerationResponse{}, fmt.Errorf("decode codex image event: %w", err)
		}
		if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
			return ImageGenerationResponse{}, fmt.Errorf("codex image generation failed: %s", event.Error.Message)
		}
		if strings.TrimSpace(event.Message) != "" && (event.Type == "error" || event.Type == "response.failed") {
			return ImageGenerationResponse{}, fmt.Errorf("codex image generation failed: %s", event.Message)
		}
		last = event
		if event.Type == "response.output_item.done" && event.Item.Type == "image_generation_call" && event.Item.Result != "" {
			return imageResponseFromBase64(event.Item.Result, event.Item.RevisedPrompt, outputFormat)
		}
	}
	if err := scanner.Err(); err != nil {
		return ImageGenerationResponse{}, fmt.Errorf("read codex image event stream: %w", err)
	}
	for _, item := range last.Response.Output {
		if item.Type == "image_generation_call" && item.Result != "" {
			return imageResponseFromBase64(item.Result, item.RevisedPrompt, outputFormat)
		}
	}
	return ImageGenerationResponse{}, fmt.Errorf("codex image response did not include an image_generation_call result")
}

func imageResponseFromBase64(payload string, revisedPrompt string, outputFormat string) (ImageGenerationResponse, error) {
	if len(payload) > maxImageResultBase64Chars {
		return ImageGenerationResponse{}, fmt.Errorf("codex image result exceeded size limit")
	}
	imageBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ImageGenerationResponse{}, fmt.Errorf("decode codex image result: %w", err)
	}
	mimeType, extension := imageOutputMime(outputFormat)
	return ImageGenerationResponse{Image: imageBytes, MimeType: mimeType, Extension: extension, RevisedPrompt: strings.TrimSpace(revisedPrompt)}, nil
}

func imageOutputMime(outputFormat string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "jpeg", "jpg":
		return "image/jpeg", "jpg"
	case "webp":
		return "image/webp", "webp"
	default:
		return "image/png", "png"
	}
}
