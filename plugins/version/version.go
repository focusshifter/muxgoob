package version

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

type VersionPlugin struct{}

func init() {
	registry.RegisterPlugin(&VersionPlugin{})
}

func (p *VersionPlugin) Start(_ interface{}) {
	if registry.Bot == nil || registry.Config.OwnerUsername == "" || database.DB == nil {
		return
	}

	versionText, err := readCurrentVersion()
	if err != nil || versionText == "" {
		if err != nil && !os.IsNotExist(err) {
			log.Printf("[version] Failed to read version file: %v", err)
		}
		return
	}

	ownerChatID, err := lookupOwnerChatID(registry.Config.OwnerUsername)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[version] Failed to look up owner %q: %v", registry.Config.OwnerUsername, err)
		}
		return
	}

	if err := notifyOwnerVersion(registry.Bot, ownerChatID, versionText); err != nil {
		log.Printf("[version] Failed to notify owner about version %s: %v", versionText, err)
	}
}

func (p *VersionPlugin) Process(message *telebot.Message) {
	// Only work in private messages
	if !message.Private() {
		return
	}

	// Check if message is !version command
	if !strings.HasPrefix(message.Text, "!version") {
		return
	}

	versionText, err := readCurrentVersion()
	if err != nil {
		// Fail silently if file not found
		return
	}

	if versionText != "" {
		registry.Bot.Send(message.Chat, versionText)
	}
}

func readCurrentVersion() (string, error) {
	content, err := os.ReadFile(".muxgoob_version")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(content)), nil
}

func lookupOwnerChatID(username string) (int64, error) {
	var ownerChatID int64
	err := database.DB.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&ownerChatID)
	if err != nil {
		return 0, err
	}

	return ownerChatID, nil
}

func notifyOwnerVersion(bot *registry.BotWrapper, ownerChatID int64, versionText string) error {
	message := fmt.Sprintf("Gooby is now running %s", versionText)
	_, err := bot.Send(&telebot.Chat{ID: ownerChatID}, message)
	return err
}
