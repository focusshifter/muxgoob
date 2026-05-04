package reply

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/tucnak/telebot"
)

const (
	imageSourceReply  = "reply"
	imageSourceLatest = "latest"
)

type ResolvedImageTarget struct {
	ChatID    int64
	MessageID int
	FileID    string
	Source    string
	Width     int
	Height    int
}

func replyReferencesPhoto(db *sql.DB, message *telebot.Message) bool {
	if message == nil || message.Chat == nil || message.ReplyTo == nil {
		return false
	}
	if message.ReplyTo.Photo != nil {
		return true
	}
	if message.ReplyTo.Chat == nil || message.ReplyTo.Chat.ID != message.Chat.ID || db == nil {
		return false
	}
	target, err := lookupImageTargetByMessageID(db, message.Chat.ID, message.ReplyTo.ID, imageSourceReply)
	return err == nil && target != nil
}

func shouldUseImageInspection(question string, message *telebot.Message, target *ResolvedImageTarget) bool {
	if target == nil {
		return false
	}
	if shouldForceSearchMessages(question) {
		return false
	}
	if message != nil && message.ReplyTo != nil {
		return target.Source == imageSourceReply
	}
	return message != nil && message.Photo != nil
}

func resolveImageTarget(db *sql.DB, question *telebot.Message) (*ResolvedImageTarget, error) {
	if db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	if question == nil || question.Chat == nil {
		return nil, nil
	}

	if question.ReplyTo != nil && question.ReplyTo.Chat != nil && question.ReplyTo.Chat.ID == question.Chat.ID {
		if target := imageTargetFromPhoto(question.Chat.ID, question.ReplyTo.ID, question.ReplyTo.Photo, imageSourceReply); target != nil {
			return target, nil
		}
		target, err := lookupImageTargetByMessageID(db, question.Chat.ID, question.ReplyTo.ID, imageSourceReply)
		if err != nil {
			return nil, err
		}
		if target != nil {
			return target, nil
		}
	}

	if target := imageTargetFromPhoto(question.Chat.ID, question.ID, question.Photo, imageSourceLatest); target != nil {
		return target, nil
	}

	return lookupLatestImageTarget(db, question.Chat.ID)
}

func imageTargetFromPhoto(chatID int64, messageID int, photo *telebot.Photo, source string) *ResolvedImageTarget {
	if photo == nil || strings.TrimSpace(photo.FileID) == "" {
		return nil
	}
	return &ResolvedImageTarget{
		ChatID:    chatID,
		MessageID: messageID,
		FileID:    photo.FileID,
		Source:    source,
		Width:     photo.Width,
		Height:    photo.Height,
	}
}

func lookupImageTargetByMessageID(db *sql.DB, chatID int64, messageID int, source string) (*ResolvedImageTarget, error) {
	var target ResolvedImageTarget
	err := db.QueryRow(`
		SELECT message_id, chat_id, file_id, width, height
		FROM media_items
		WHERE chat_id = ? AND message_id = ? AND type = 'photo' AND file_id <> ''
		LIMIT 1`, chatID, messageID).Scan(&target.MessageID, &target.ChatID, &target.FileID, &target.Width, &target.Height)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	target.Source = source
	return &target, nil
}

func lookupLatestImageTarget(db *sql.DB, chatID int64) (*ResolvedImageTarget, error) {
	var target ResolvedImageTarget
	err := db.QueryRow(`
		SELECT mi.message_id, mi.chat_id, mi.file_id, mi.width, mi.height
		FROM media_items mi
		JOIN messages m ON m.id = mi.message_id AND m.chat_id = mi.chat_id
		WHERE mi.chat_id = ? AND mi.type = 'photo' AND mi.file_id <> ''
		ORDER BY m.unixtime DESC, mi.message_id DESC
		LIMIT 1`, chatID).Scan(&target.MessageID, &target.ChatID, &target.FileID, &target.Width, &target.Height)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	target.Source = imageSourceLatest
	return &target, nil
}
