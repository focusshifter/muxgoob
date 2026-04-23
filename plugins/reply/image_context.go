package reply

import (
	"database/sql"
	"fmt"
	"regexp"
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

var imageQuestionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(картинк|картинка|фото|фотке|фотка|пикч|изображен|изображено|изображении|image|picture|photo)`),
	regexp.MustCompile(`(?i)(мем|meme)`),
	regexp.MustCompile(`(?i)(что там|what'?s there|what is there|what's in)`),
	regexp.MustCompile(`(?i)(объясни|explain).*(мем|meme|картинк|image|picture)`),
	regexp.MustCompile(`(?i)(найс картинка|nice picture|nice image)`),
}

func shouldForceInspectRecentImage(question string) bool {
	question = strings.TrimSpace(question)
	if question == "" {
		return false
	}
	for _, pattern := range imageQuestionPatterns {
		if pattern.MatchString(question) {
			return true
		}
	}
	return false
}

func shouldReturnMissingImageFallback(question string, message *telebot.Message, replyToPhoto bool) bool {
	if shouldForceInspectRecentImage(question) {
		return true
	}
	return replyToPhoto
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

func hasAlternateNonImageContext(message *telebot.Message, replyToPhoto bool) bool {
	if message == nil {
		return false
	}
	if replyToPhoto {
		return false
	}
	if strings.TrimSpace(message.Text) != "" || strings.TrimSpace(message.Caption) != "" {
		if message.ReplyTo == nil {
			return false
		}
		if strings.TrimSpace(message.ReplyTo.Text) != "" || strings.TrimSpace(message.ReplyTo.Caption) != "" {
			return true
		}
	}
	return false
}

func resolveImageTarget(db *sql.DB, question *telebot.Message) (*ResolvedImageTarget, error) {
	if db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	if question == nil || question.Chat == nil {
		return nil, nil
	}

	if question.ReplyTo != nil && question.ReplyTo.Chat != nil && question.ReplyTo.Chat.ID == question.Chat.ID {
		target, err := lookupImageTargetByMessageID(db, question.Chat.ID, question.ReplyTo.ID, imageSourceReply)
		if err != nil {
			return nil, err
		}
		if target != nil {
			return target, nil
		}
	}

	return lookupLatestImageTarget(db, question.Chat.ID)
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
