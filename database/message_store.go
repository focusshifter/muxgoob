package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tucnak/telebot"
)

// SaveIncomingMessage atomically upserts one Telegram message and replaces its
// child entities/media. It is safe to call again for duplicate deliveries or
// edited messages.
func SaveIncomingMessage(ctx context.Context, db *sql.DB, message *telebot.Message) (bool, error) {
	if db == nil {
		return false, errors.New("database is not initialized")
	}
	if message == nil || message.Chat == nil {
		return false, errors.New("message and chat are required")
	}

	messageData, err := json.Marshal(message)
	if err != nil {
		return false, fmt.Errorf("marshal message: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin message transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var senderID interface{}
	if message.Sender != nil {
		senderID = message.Sender.ID
	}
	users := []*telebot.User{message.Sender, message.OriginalSender}
	for _, entity := range append(append([]telebot.MessageEntity(nil), message.Entities...), message.CaptionEntities...) {
		users = append(users, entity.User)
	}
	for _, user := range users {
		if err := upsertTelegramUser(ctx, tx, user); err != nil {
			return false, err
		}
	}
	if err := upsertTelegramChat(ctx, tx, message.Chat); err != nil {
		return false, err
	}
	if err := upsertTelegramChat(ctx, tx, message.OriginalChat); err != nil {
		return false, err
	}

	messageResult, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			id, chat_id, sender_id, reply_to_message_id, forward_from_id,
			forward_from_chat_id, forward_date, edit_date, media_group_id,
			author_signature, unixtime, text, caption, data
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_id) DO UPDATE SET
			sender_id=excluded.sender_id,
			reply_to_message_id=excluded.reply_to_message_id,
			forward_from_id=excluded.forward_from_id,
			forward_from_chat_id=excluded.forward_from_chat_id,
			forward_date=excluded.forward_date,
			edit_date=excluded.edit_date,
			media_group_id=excluded.media_group_id,
			author_signature=excluded.author_signature,
			unixtime=excluded.unixtime,
			text=excluded.text,
			caption=excluded.caption,
			data=excluded.data
		WHERE messages.data <> excluded.data`,
		message.ID, message.Chat.ID, senderID,
		messageID(message.ReplyTo), userID(message.OriginalSender),
		chatID(message.OriginalChat), message.OriginalUnixtime, message.LastEdit,
		message.AlbumID, message.Signature, message.Time().Unix(),
		message.Text, message.Caption, string(messageData))
	if err != nil {
		return false, fmt.Errorf("upsert message: %w", err)
	}
	changed, err := messageResult.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect message upsert: %w", err)
	}
	if changed == 0 {
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("commit duplicate message transaction: %w", err)
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_entities WHERE message_id=? AND chat_id=?`, message.ID, message.Chat.ID); err != nil {
		return false, fmt.Errorf("replace message entities: %w", err)
	}
	if err := insertEntities(ctx, tx, message, message.Entities, false); err != nil {
		return false, err
	}
	if err := insertEntities(ctx, tx, message, message.CaptionEntities, true); err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM media_items WHERE message_id=? AND chat_id=?`, message.ID, message.Chat.ID); err != nil {
		return false, fmt.Errorf("replace media items: %w", err)
	}
	if message.Photo != nil {
		photoData, err := json.Marshal(message.Photo)
		if err != nil {
			return false, fmt.Errorf("marshal photo: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_items (
				message_id, chat_id, type, file_id, file_unique_id,
				width, height, file_size, data
			) VALUES (?, ?, 'photo', ?, '', ?, ?, ?, ?)`,
			message.ID, message.Chat.ID, message.Photo.FileID,
			message.Photo.Width, message.Photo.Height, message.Photo.FileSize,
			string(photoData)); err != nil {
			return false, fmt.Errorf("insert photo: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit message transaction: %w", err)
	}
	return true, nil
}

func upsertTelegramUser(ctx context.Context, tx *sql.Tx, user *telebot.User) error {
	if user == nil {
		return nil
	}
	data, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("marshal user %d: %w", user.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id,username,first_name,last_name,data) VALUES (?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET username=excluded.username,first_name=excluded.first_name,last_name=excluded.last_name,data=excluded.data`, user.ID, user.Username, user.FirstName, user.LastName, string(data)); err != nil {
		return fmt.Errorf("upsert user %d: %w", user.ID, err)
	}
	return nil
}

func upsertTelegramChat(ctx context.Context, tx *sql.Tx, chat *telebot.Chat) error {
	if chat == nil {
		return nil
	}
	data, err := json.Marshal(chat)
	if err != nil {
		return fmt.Errorf("marshal chat %d: %w", chat.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chats (id,type,title,username,first_name,last_name,data) VALUES (?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET type=excluded.type,title=excluded.title,username=excluded.username,first_name=excluded.first_name,last_name=excluded.last_name,data=excluded.data`, chat.ID, chat.Type, chat.Title, chat.Username, chat.FirstName, chat.LastName, string(data)); err != nil {
		return fmt.Errorf("upsert chat %d: %w", chat.ID, err)
	}
	return nil
}

func insertEntities(ctx context.Context, tx *sql.Tx, message *telebot.Message, entities []telebot.MessageEntity, isCaption bool) error {
	for _, entity := range entities {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_entities (
				message_id, chat_id, type, offset, length, url, user_id, language, is_caption
			) VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)`,
			message.ID, message.Chat.ID, entity.Type, entity.Offset, entity.Length,
			entity.URL, userID(entity.User), isCaption); err != nil {
			return fmt.Errorf("insert message entity: %w", err)
		}
	}
	return nil
}

func messageID(message *telebot.Message) interface{} {
	if message == nil {
		return nil
	}
	return message.ID
}

func userID(user *telebot.User) interface{} {
	if user == nil {
		return nil
	}
	return user.ID
}

func chatID(chat *telebot.Chat) interface{} {
	if chat == nil {
		return nil
	}
	return chat.ID
}
