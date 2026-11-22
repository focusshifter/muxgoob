package tools

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/registry"
)

// deliverImage only retries definite photo-processing rejections. Transport errors
// may mean Telegram accepted a message, so retrying those could create duplicates.
func (t *GenerateImageTool) deliverImage(original, caption string) error {
	sender := t.send
	if sender == nil {
		sender = sendImageToChat
	}
	firstErr := sender(t.chatID, original, caption)
	if firstErr == nil {
		return nil
	}
	if !isTelegramPhotoProcessingError(firstErr) {
		return fmt.Errorf("image delivery failed (original photo; delivery may be unconfirmed): %w", firstErr)
	}
	log.Printf("[tools] generateImage photo rejected chat_id=%d path=%q: %v; preparing clean JPEG", t.chatID, original, firstErr)
	cleaned, err := reencodeImageJPEG(original)
	if err != nil {
		log.Printf("[tools] generateImage clean JPEG preparation failed chat_id=%d path=%q: %v; falling back to original document", t.chatID, original, err)
		document := &telebot.Document{File: telebot.FromDisk(original), Caption: caption}
		if sendErr := sendGeneratedMedia(t.chatID, document, "document", original); sendErr != nil {
			return fmt.Errorf("image delivery failed (original document after JPEG file preparation failed: %v; original photo rejection: %v): %w", err, firstErr, sendErr)
		}
		return nil
	}
	retryErr := sender(t.chatID, cleaned, caption)
	if retryErr == nil {
		return nil
	}
	if !isTelegramPhotoProcessingError(retryErr) {
		return fmt.Errorf("image delivery failed (clean JPEG photo; original rejection: %v; delivery may be unconfirmed): %w", firstErr, retryErr)
	}
	log.Printf("[tools] generateImage clean JPEG rejected chat_id=%d path=%q: %v; falling back to original document", t.chatID, cleaned, retryErr)
	document := &telebot.Document{File: telebot.FromDisk(original), Caption: caption}
	if err := sendGeneratedMedia(t.chatID, document, "document", original); err != nil {
		return fmt.Errorf("image delivery failed (original document after original photo rejection: %v; clean JPEG photo rejection: %v): %w", firstErr, retryErr, err)
	}
	return nil
}

// This Telebot version exposes API rejections as plain errors, not typed errors.
// Match complete API descriptions, never a substring in an arbitrary timeout.
func isTelegramPhotoProcessingError(err error) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		switch strings.ToLower(strings.TrimSpace(err.Error())) {
		case "api error: bad request: image_process_failed",
			"api error: bad request: photo_invalid_dimensions",
			"api error: bad request: photo_invalid":
			return true
		}
	}
	return false
}

func sendGeneratedMedia(chatID int64, media interface{}, method, path string) error {
	if registry.Bot == nil {
		return fmt.Errorf("bot is not initialized")
	}
	message, err := registry.Bot.Send(&imageRecipient{chatID: chatID}, media)
	if err != nil {
		log.Printf("[tools] generateImage delivery failed method=%s chat_id=%d path=%q: %v", method, chatID, path, err)
		return err
	}
	if message != nil {
		log.Printf("[tools] generateImage delivered method=%s chat_id=%d message_id=%d path=%q", method, chatID, message.ID, path)
	} else {
		log.Printf("[tools] generateImage delivered method=%s chat_id=%d message_id=unavailable path=%q", method, chatID, path)
	}
	return nil
}

// reencodeImageJPEG strips provider metadata and flattens transparency onto white.
// The original is never modified; failed partial output is removed.
func reencodeImageJPEG(original string) (path string, err error) {
	source, err := os.Open(original)
	if err != nil {
		return "", fmt.Errorf("open original image: %w", err)
	}
	defer source.Close()
	decoded, _, err := image.Decode(source)
	if err != nil {
		return "", fmt.Errorf("decode original image: %w", err)
	}
	bounds := decoded.Bounds()
	flattened := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(flattened, flattened.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flattened, flattened.Bounds(), decoded, bounds.Min, draw.Over)
	output, err := os.CreateTemp(filepath.Dir(original), "gooby-clean-*.jpg")
	if err != nil {
		return "", fmt.Errorf("create clean JPEG: %w", err)
	}
	path = output.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(output.Name())
		}
	}()
	if encodeErr := jpeg.Encode(output, flattened, &jpeg.Options{Quality: 90}); encodeErr != nil {
		_ = output.Close()
		return "", fmt.Errorf("encode clean JPEG: %w", encodeErr)
	}
	if err = output.Close(); err != nil {
		return "", fmt.Errorf("close clean JPEG: %w", err)
	}
	return path, nil
}
