package reply

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"
	"golang.org/x/image/draw"

	"github.com/focusshifter/muxgoob/registry"
)

const (
	defaultImageAiModel         = "google/gemini-3.1-flash-lite-preview"
	maxVisionImageDimension     = 768
	defaultVisionMaxTokens      = 180
	visionFallbackNoImageMsg    = "Не вижу рядом картинки — реплайнись на неё или закинь ещё раз."
	imageInspectionContextIntro = "Контекст по картинке:"
)

var inspectRecentImageQuestion = func(message *telebot.Message, target *ResolvedImageTarget) (string, error) {
	if message == nil || target == nil {
		return "", nil
	}

	downloadedPath, err := downloadTelegramImage(target.FileID)
	if err != nil {
		return "", err
	}
	defer os.Remove(downloadedPath)

	processedPath, err := preprocessVisionImage(downloadedPath)
	if err != nil {
		return "", err
	}
	defer os.Remove(processedPath)

	return analyzeImageWithVision(message, processedPath)
}

var downloadTelegramImage = func(fileID string) (string, error) {
	if strings.TrimSpace(fileID) == "" {
		return "", fmt.Errorf("image file id is empty")
	}
	if registry.Bot == nil || registry.Bot.Bot == nil {
		return "", fmt.Errorf("telegram bot is not initialized")
	}

	tmpDir, err := os.MkdirTemp("", "gooby-image-download-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(tmpDir, "image")
	file := telebot.File{FileID: fileID}
	if err := registry.Bot.Bot.Download(&file, path); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	return path, nil
}

var analyzeImageWithVision = func(message *telebot.Message, imagePath string) (string, error) {
	if message == nil || message.Chat == nil {
		return "", fmt.Errorf("message chat is missing")
	}
	if strings.TrimSpace(registry.Config.OpenrouterApiKey) == "" {
		return "", fmt.Errorf("openrouter api key is not configured")
	}

	chatID := message.Chat.ID
	chatIDPtr := &chatID
	model := strings.TrimSpace(registry.GetImageAiModel(chatIDPtr))
	if model == "" {
		model = defaultImageAiModel
	}

	dataURL, err := buildVisionDataURL(imagePath)
	if err != nil {
		return "", err
	}

	config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
	config.BaseURL = "https://openrouter.ai/api/v1"
	client := openai.NewClientWithConfig(config)

	question := strings.TrimSpace(message.Text)
	if question == "" {
		question = strings.TrimSpace(message.Caption)
	}
	if question == "" {
		question = "Что на этой картинке?"
	}

	resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:       model,
		Temperature: 0.1,
		MaxTokens:   defaultVisionMaxTokens,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "You answer brief conversationally in Russian. Answer the user's question about the image or meme in 1-3 short sentences. Mention visible text only if relevant. Do not use markdown.",
			},
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{Type: openai.ChatMessagePartTypeText, Text: question},
					{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: dataURL, Detail: openai.ImageURLDetailLow}},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("vision completion returned no choices")
	}
	answer := strings.TrimSpace(resp.Choices[0].Message.Content)
	if answer == "" {
		return "", fmt.Errorf("vision completion returned empty content")
	}
	return answer, nil
}

func maybeBuildImageInspectionContext(message *telebot.Message, question string) (string, string, bool) {
	question = strings.TrimSpace(question)
	forceInspect := shouldForceInspectRecentImage(question)
	replyToPhoto := replyReferencesPhoto(sqliteDb, message)
	forceFallback := shouldReturnMissingImageFallback(question, message, replyToPhoto)

	target, err := resolveImageTarget(sqliteDb, message)
	if err != nil {
		return "", "", false
	}
	if target == nil {
		if forceInspect || forceFallback {
			return "", visionFallbackNoImageMsg, true
		}
		return "", "", false
	}
	if forceInspect && (message == nil || (message.ReplyTo == nil && message.Photo == nil)) && target.Source != imageSourceReply {
		return "", visionFallbackNoImageMsg, true
	}
	if message != nil && message.ReplyTo != nil && replyToPhoto && target.Source != imageSourceReply {
		return "", visionFallbackNoImageMsg, true
	}
	if !shouldUseImageInspection(question, message, target) {
		return "", "", false
	}

	answer, err := inspectRecentImageQuestion(message, target)
	if err != nil {
		return "", visionFallbackNoImageMsg, true
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", visionFallbackNoImageMsg, true
	}

	context := strings.TrimSpace(strings.Join([]string{
		imageInspectionContextIntro,
		"- Вопрос пользователя: " + question,
		"- Краткое описание/разбор изображения: " + answer,
		"Используй это только как входной факт-контекст про изображение. Сформулируй финальный ответ сам, в стиле gooby.",
	}, "\n"))
	return context, "", true
}

func preprocessVisionImage(srcPath string) (string, error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	src, _, err := image.Decode(file)
	if err != nil {
		return "", err
	}

	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	targetWidth := width
	targetHeight := height
	if width > maxVisionImageDimension || height > maxVisionImageDimension {
		if width >= height {
			targetWidth = maxVisionImageDimension
			targetHeight = max(1, height*maxVisionImageDimension/width)
		} else {
			targetHeight = maxVisionImageDimension
			targetWidth = max(1, width*maxVisionImageDimension/height)
		}
	}

	dstImage := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(dstImage, dstImage.Bounds(), src, bounds, draw.Over, nil)

	tmpDir, err := os.MkdirTemp("", "gooby-image-vision-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(tmpDir, "image.jpg")
	out, err := os.Create(path)
	if err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	defer out.Close()

	if err := jpeg.Encode(out, dstImage, &jpeg.Options{Quality: 80}); err != nil {
		os.RemoveAll(tmpDir)
		return "", err
	}
	return path, nil
}

func buildVisionDataURL(imagePath string) (string, error) {
	bytes, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(bytes)
	return "data:image/jpeg;base64," + encoded, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
