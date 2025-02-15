package admin

import (
	"fmt"
	"strings"

	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/database"
	"github.com/focusshifter/muxgoob/registry"
)

type AdminPlugin struct {}

func init() {
	registry.RegisterPlugin(&AdminPlugin{})
}

func (p *AdminPlugin) Start(config interface{}) {}

func (p *AdminPlugin) Process(message *telebot.Message) {
	// Only process private messages from the owner
	if message.Chat.Type != telebot.ChatPrivate || 
		message.Sender.Username != registry.Config.OwnerUsername {
		return
	}

	// Check for /list command
	if message.Text == "/list" {
		bot := registry.Bot
		
		// Query all chats from the database
		rows, err := database.DB.Query(`
			SELECT id, type, title, username, first_name, last_name 
			FROM chats 
			ORDER BY COALESCE(title, username, first_name || ' ' || last_name) ASC
		`)
		if err != nil {
			bot.Send(message.Chat, "Error querying chats: "+err.Error())
			return
		}
		defer rows.Close()

		var chats []string
		for rows.Next() {
			var (
				id                                    int64
				chatType, title, username, firstName, lastName string
			)
			if err := rows.Scan(&id, &chatType, &title, &username, &firstName, &lastName); err != nil {
				bot.Send(message.Chat, "Error scanning chat row: "+err.Error())
				return
			}

			chatName := title
			if chatName == "" {
				if chatType == "private" {
					chatName = username
					if chatName == "" {
						chatName = strings.TrimSpace(fmt.Sprintf("%s %s", firstName, lastName))
					}
				}
			}

			chats = append(chats, fmt.Sprintf("Chat: %s (ID: %d, Type: %s)", chatName, id, chatType))
		}

		if len(chats) == 0 {
			bot.Send(message.Chat, "No chats found in database")
			return
		}

		// Send the list of chats
		response := "List of chats:\n\n" + strings.Join(chats, "\n")
		bot.Send(message.Chat, response)
		return
	}
}
