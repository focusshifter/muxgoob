package reply

import (
	chattools "github.com/focusshifter/muxgoob/internal/tools"
	"github.com/tucnak/telebot"
)

func appendReactionReplyTool(message *telebot.Message, tools []chattools.Tool, parts []string) ([]chattools.Tool, []string, *chattools.ReactToMessageTool) {
	if message == nil || message.Chat == nil || message.Chat.ID == 0 || message.ID <= 0 {
		return tools, parts, nil
	}
	tool := chattools.NewReactToMessageTool(message)
	tools = append(tools, tool)
	parts = append(parts, "A reaction instead of text is optional: use reactToMessage for a simple acknowledgement or an obvious emotional response when a textual reply would add nothing. Decide semantically from the current message and context, not message length. Do not use it instead of answering substantive questions, carrying out tasks, generating requested images/polls, or giving needed explanations. It must be the sole reply, not an extra acknowledgement before other tools. A successful reaction ends this turn without text. On failure, continue normally and do not claim that a reaction was sent.")
	return tools, parts, tool
}
