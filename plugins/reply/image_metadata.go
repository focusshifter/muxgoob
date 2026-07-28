package reply

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

const defaultImageMetadataMaxPerMinute = 6

type ImageMetadataDescription struct {
	Model       string   `json:"model"`
	Description string   `json:"description"`
	VisibleText string   `json:"visible_text"`
	MemeOrJoke  string   `json:"meme_or_joke"`
	Tags        []string `json:"tags"`
}

var describeImageForMetadata = realDescribeImageForMetadata

var imageMetadataLimiter = struct {
	sync.Mutex
	windowStart time.Time
	count       int
}{}

func maybeQueueImageMetadata(message *telebot.Message) {
	if message == nil || message.Chat == nil || message.ID == 0 || message.Photo == nil || strings.TrimSpace(message.Photo.FileID) == "" || sqliteDb == nil {
		return
	}
	if !isImageMetadataEnabled() || !allowImageMetadataEnrichment() {
		return
	}
	if imageMetadataExists(sqliteDb, message.Chat.ID, message.ID, message.Photo.FileID) {
		return
	}
	messageCopy := *message
	photoCopy := *message.Photo
	messageCopy.Photo = &photoCopy
	go func(msg telebot.Message) {
		if err := describeAndStoreImageMetadata(&msg); err != nil {
			log.Printf("[reply/image-metadata] failed chat=%d msg=%d: %v", msg.Chat.ID, msg.ID, err)
		}
	}(messageCopy)
}

func isImageMetadataEnabled() bool {
	if registry.Config.ImageMetadataEnabled == nil {
		return true
	}
	return *registry.Config.ImageMetadataEnabled
}

func allowImageMetadataEnrichment() bool {
	maxPerMinute := registry.Config.ImageMetadataMaxPerMinute
	if maxPerMinute <= 0 {
		maxPerMinute = defaultImageMetadataMaxPerMinute
	}
	now := time.Now()
	imageMetadataLimiter.Lock()
	defer imageMetadataLimiter.Unlock()
	if imageMetadataLimiter.windowStart.IsZero() || now.Sub(imageMetadataLimiter.windowStart) >= time.Minute {
		imageMetadataLimiter.windowStart = now
		imageMetadataLimiter.count = 0
	}
	if imageMetadataLimiter.count >= maxPerMinute {
		return false
	}
	imageMetadataLimiter.count++
	return true
}

func describeAndStoreImageMetadata(message *telebot.Message) error {
	if message == nil || message.Chat == nil || message.Photo == nil {
		return nil
	}
	fileID := strings.TrimSpace(message.Photo.FileID)
	if fileID == "" || sqliteDb == nil {
		return nil
	}
	if imageMetadataExists(sqliteDb, message.Chat.ID, message.ID, fileID) {
		return nil
	}
	target := imageTargetFromPhoto(message.Chat.ID, message.ID, message.Photo, imageSourceLatest)
	if target == nil {
		return nil
	}
	metadata, err := describeImageForMetadata(message, target)
	if err != nil {
		return err
	}
	metadata.Description = strings.TrimSpace(metadata.Description)
	metadata.VisibleText = strings.TrimSpace(metadata.VisibleText)
	metadata.MemeOrJoke = strings.TrimSpace(metadata.MemeOrJoke)
	if metadata.Description == "" && metadata.VisibleText == "" && metadata.MemeOrJoke == "" {
		return fmt.Errorf("image metadata description is empty")
	}
	if metadata.Model == "" {
		metadata.Model = defaultImageAiModel
	}
	return saveImageMetadata(sqliteDb, message.Chat.ID, message.ID, fileID, metadata)
}

func imageMetadataExists(db *sql.DB, chatID int64, messageID int, fileID string) bool {
	if db == nil || strings.TrimSpace(fileID) == "" {
		return false
	}
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM media_metadata WHERE chat_id = ? AND message_id = ? AND file_id = ? AND status = 'done'`, chatID, messageID, fileID).Scan(&count)
	return err == nil && count > 0
}

func saveImageMetadata(db *sql.DB, chatID int64, messageID int, fileID string, metadata ImageMetadataDescription) error {
	if db == nil {
		return fmt.Errorf("database is not initialized")
	}
	description := strings.TrimSpace(strings.Join([]string{metadata.Description, metadata.MemeOrJoke}, " "))
	if description == "" {
		description = strings.TrimSpace(metadata.VisibleText)
	}
	_, err := db.Exec(`INSERT INTO media_metadata (
		message_id, chat_id, media_type, file_id, file_unique_id, model, description, visible_text, tags, status, error, updated_at
	) VALUES (?, ?, 'photo', ?, '', ?, ?, ?, ?, 'done', '', strftime('%s', 'now'))
	ON CONFLICT(chat_id, message_id, file_id) DO UPDATE SET
		model = excluded.model,
		description = excluded.description,
		visible_text = excluded.visible_text,
		tags = excluded.tags,
		status = 'done',
		error = '',
		updated_at = strftime('%s', 'now')`,
		messageID, chatID, fileID, metadata.Model, description, metadata.VisibleText, strings.Join(metadata.Tags, ","))
	return err
}

func realDescribeImageForMetadata(message *telebot.Message, target *ResolvedImageTarget) (ImageMetadataDescription, error) {
	if message == nil || message.Chat == nil || target == nil {
		return ImageMetadataDescription{}, nil
	}
	if strings.TrimSpace(registry.Config.OpenrouterApiKey) == "" {
		return ImageMetadataDescription{}, fmt.Errorf("openrouter api key is not configured")
	}

	downloadedPath, err := downloadTelegramImage(target.FileID)
	if err != nil {
		return ImageMetadataDescription{}, err
	}
	defer os.Remove(downloadedPath)

	processedPath, err := preprocessVisionImage(downloadedPath)
	if err != nil {
		return ImageMetadataDescription{}, err
	}
	defer os.Remove(processedPath)

	dataURL, err := buildVisionDataURL(processedPath)
	if err != nil {
		return ImageMetadataDescription{}, err
	}

	chatID := message.Chat.ID
	model := strings.TrimSpace(registry.GetImageVisionModel(&chatID))
	if model == "" {
		model = defaultImageAiModel
	}

	config := openai.DefaultConfig(registry.Config.OpenrouterApiKey)
	config.BaseURL = "https://openrouter.ai/api/v1"
	client := openai.NewClientWithConfig(config)

	resp, err := client.CreateChatCompletion(context.Background(), buildImageMetadataCompletionRequest(model, dataURL))
	if err != nil {
		return ImageMetadataDescription{}, err
	}
	if len(resp.Choices) == 0 {
		return ImageMetadataDescription{}, fmt.Errorf("vision completion returned no choices")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return ImageMetadataDescription{}, fmt.Errorf("vision completion returned empty content")
	}

	metadata := ImageMetadataDescription{Model: model}
	if err := json.Unmarshal([]byte(stripJSONFence(content)), &metadata); err != nil {
		metadata.Description = content
	}
	metadata.Model = model
	return metadata, nil
}

func buildImageMetadataCompletionRequest(model, dataURL string) openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{
		Model:       model,
		Temperature: 0.1,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "Describe Telegram images for chat-history retrieval. Return Russian JSON with keys: description, visible_text, meme_or_joke, tags. Be factual and detailed enough to preserve useful visual information for future search. Mention visible text exactly. Do not invent context outside the image.",
			},
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{Type: openai.ChatMessagePartTypeText, Text: "Опиши эту картинку как метадату сообщения для поиска и будущего контекста."},
					{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: dataURL, Detail: openai.ImageURLDetailLow}},
				},
			},
		},
	}
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func imageMetadataForMessage(message telebot.Message) string {
	if sqliteDb == nil || message.Chat == nil || message.ID == 0 {
		return ""
	}
	rows, err := sqliteDb.Query(`SELECT description, visible_text FROM media_metadata WHERE chat_id = ? AND message_id = ? AND status = 'done' ORDER BY created_at, file_id`, message.Chat.ID, message.ID)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var description, visibleText sql.NullString
		if err := rows.Scan(&description, &visibleText); err != nil {
			continue
		}
		text := strings.TrimSpace(description.String)
		if visible := strings.TrimSpace(visibleText.String); visible != "" {
			text = strings.TrimSpace(text + " Visible text: " + visible)
		}
		if text != "" {
			parts = append(parts, truncateText(text, 500))
		}
	}
	return strings.Join(parts, " | ")
}
