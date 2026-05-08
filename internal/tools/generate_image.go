package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/internal/openaicodex"
	"github.com/focusshifter/muxgoob/registry"
)

type ImageGenerator interface {
	GenerateImage(ctx context.Context, request openaicodex.ImageGenerationRequest) (openaicodex.ImageGenerationResponse, error)
}

type GenerateImageTool struct {
	chatID    int64
	sent      bool
	generator ImageGenerator
	send      func(chatID int64, imagePath string, caption string) error
	notify    func(chatID int64, action telebot.ChatAction) error
	outputDir string
}

type generateImageArgs struct {
	Prompt       string `json:"prompt"`
	Caption      string `json:"caption,omitempty"`
	Model        string `json:"model,omitempty"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
}

type generateImageResult struct {
	Sent          bool   `json:"sent"`
	Model         string `json:"model"`
	Size          string `json:"size"`
	Quality       string `json:"quality,omitempty"`
	OutputFormat  string `json:"output_format"`
	Path          string `json:"path"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

func NewGenerateImageTool(chatID int64) *GenerateImageTool {
	return &GenerateImageTool{chatID: chatID, generator: openaicodex.NewClient()}
}

func (t *GenerateImageTool) Definition() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        "generateImage",
			Description: "Generate an image with OpenAI Codex OAuth using GPT Image (default gpt-image-2) and send it directly to the current Telegram chat. Use this when the user asks to draw, generate, create, or render a picture/image/photo/sticker/мем/картинку. After using this tool successfully, do not send follow-up text.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "Detailed image prompt. Preserve the user's language and requested style/content. This is only for image generation and will not be printed to the chat.",
					},
					"caption": map[string]any{
						"type":        "string",
						"description": "Optional short Telegram caption related to the generated image, in the user's tone/language. Do not put the full prompt here. Leave empty if no natural caption is useful.",
					},
					"model": map[string]any{
						"type":        "string",
						"description": "Optional model override. Default: gpt-image-2. For true transparent OpenAI backgrounds use gpt-image-1.5 instead.",
					},
					"size": map[string]any{
						"type":        "string",
						"description": "Telegram-optimized output size. Default 1024x1024. Prefer 1024x1024 for square, 1536x1024 for landscape, or 1024x1536 for portrait. Do not request 2K/4K sizes unless the user explicitly asks for high resolution.",
					},
					"quality": map[string]any{
						"type":        "string",
						"description": "Optional quality: low, medium, high, or auto.",
					},
					"output_format": map[string]any{
						"type":        "string",
						"description": "Output format: png, jpeg, or webp. Default png.",
					},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

func (t *GenerateImageTool) Execute(ctx context.Context, args string) (string, error) {
	parsedArgs := generateImageArgs{}
	if err := json.Unmarshal([]byte(args), &parsedArgs); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	prompt := strings.TrimSpace(parsedArgs.Prompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	model := cleanImageOption(parsedArgs.Model)
	if model == "" {
		model = "gpt-image-2"
	}
	size := telegramImageSize(cleanImageOption(parsedArgs.Size))
	if size == "" {
		size = "1024x1024"
	}
	quality := cleanImageOption(parsedArgs.Quality)
	outputFormat := strings.ToLower(cleanImageOption(parsedArgs.OutputFormat))
	if outputFormat == "" {
		outputFormat = "png"
	}
	if outputFormat != "png" && outputFormat != "jpeg" && outputFormat != "jpg" && outputFormat != "webp" {
		return "", fmt.Errorf("unsupported output_format %q", outputFormat)
	}

	generator := t.generator
	if generator == nil {
		generator = openaicodex.NewClient()
	}
	stopTyping := t.startActionKeepalive(ctx, telebot.Typing, 2*time.Second)
	result, err := generator.GenerateImage(ctx, openaicodex.ImageGenerationRequest{
		Prompt:       prompt,
		Model:        model,
		Size:         size,
		Quality:      quality,
		OutputFormat: outputFormat,
	})
	stopTyping()
	if err != nil {
		return "", err
	}
	path, err := t.writeImage(result.Image, result.Extension)
	if err != nil {
		return "", err
	}
	caption := truncateCaption(parsedArgs.Caption)
	sender := t.send
	if sender == nil {
		sender = sendImageToChat
	}
	// Chat actions are best-effort only; failing to show "uploading photo" should not
	// prevent delivery of the generated image.
	_ = t.notifyAction(telebot.UploadingPhoto)
	if err := sender(t.chatID, path, caption); err != nil {
		return "", err
	}
	t.sent = true
	return marshalJSON(generateImageResult{Sent: true, Model: result.Model, Size: size, Quality: quality, OutputFormat: outputFormat, Path: path, RevisedPrompt: result.RevisedPrompt}), nil
}

func (t *GenerateImageTool) WasSent() bool {
	return t != nil && t.sent
}

func (t *GenerateImageTool) startActionKeepalive(ctx context.Context, action telebot.ChatAction, interval time.Duration) func() {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	done := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() { close(done) })
	}
	_ = t.notifyAction(action)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				_ = t.notifyAction(action)
			}
		}
	}()
	return stop
}

func (t *GenerateImageTool) notifyAction(action telebot.ChatAction) error {
	if t == nil {
		return nil
	}
	notify := t.notify
	if notify == nil {
		notify = notifyChatAction
	}
	return notify(t.chatID, action)
}

func (t *GenerateImageTool) writeImage(data []byte, extension string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("generated image is empty")
	}
	extension = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(extension)), ".")
	if extension == "" {
		extension = "png"
	}
	outputDir := strings.TrimSpace(t.outputDir)
	if outputDir == "" {
		outputDir = filepath.Join(os.TempDir(), "muxgoob-generated-images")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create image output dir: %w", err)
	}
	path := filepath.Join(outputDir, fmt.Sprintf("gooby-%d.%s", time.Now().UnixNano(), extension))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write generated image: %w", err)
	}
	return path, nil
}

func sendImageToChat(chatID int64, imagePath string, caption string) error {
	if registry.Bot == nil {
		return fmt.Errorf("bot is not initialized")
	}
	photo := &telebot.Photo{File: telebot.FromDisk(imagePath), Caption: caption}
	_, err := registry.Bot.Send(&imageRecipient{chatID: chatID}, photo)
	return err
}

func notifyChatAction(chatID int64, action telebot.ChatAction) error {
	if registry.Bot == nil {
		return nil
	}
	return registry.Bot.Notify(&imageRecipient{chatID: chatID}, action)
}

type imageRecipient struct {
	chatID int64
}

func (r *imageRecipient) Recipient() string {
	return fmt.Sprintf("%d", r.chatID)
}

func cleanImageOption(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\u2028', '\u2029':
			return ' '
		default:
			return r
		}
	}, value)
	return strings.TrimSpace(cleaned)
}

func telegramImageSize(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "2048x2048", "4096x4096":
		return "1024x1024"
	case "2048x1152", "3840x2160":
		return "1536x1024"
	case "1152x2048", "2160x3840":
		return "1024x1536"
	default:
		return size
	}
}

func truncateCaption(caption string) string {
	runes := []rune(strings.TrimSpace(caption))
	if len(runes) <= 900 {
		return string(runes)
	}
	return string(runes[:897]) + "…"
}
