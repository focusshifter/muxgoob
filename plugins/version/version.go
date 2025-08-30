package version

import (
	"io/ioutil"
	"strings"

	"github.com/focusshifter/muxgoob/registry"
	"github.com/tucnak/telebot"
)

type VersionPlugin struct{}

func init() {
	registry.RegisterPlugin(&VersionPlugin{})
}

func (p *VersionPlugin) Start(_ interface{}) {}

func (p *VersionPlugin) Process(message *telebot.Message) {
	// Only work in private messages
	if !message.Private() {
		return
	}

	// Check if message is !version command
	if !strings.HasPrefix(message.Text, "!version") {
		return
	}

	// Read version from .muxgoob_version file
	content, err := ioutil.ReadFile(".muxgoob_version")
	if err != nil {
		// Fail silently if file not found
		return
	}

	// Send version to user
	versionText := strings.TrimSpace(string(content))
	if versionText != "" {
		registry.Bot.Send(message.Chat, versionText)
	}
}