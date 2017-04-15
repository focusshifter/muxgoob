package dupelink

import (
	"net/url"
	"strconv"
	"time"
	"log"

	"github.com/tucnak/telebot"
	"github.com/asdine/storm"

	"github.com/focusshifter/muxgoob/registry"
)

type DupeLinkPlugin struct {
}

type DupeLink struct {
	ID int `storm:"id,increment"`
	URL string `storm:"index"`
	MessageID int
	Sender telebot.User
	Unixtime int
}

var db *storm.DB

func init() {
	registry.RegisterPlugin(&DupeLinkPlugin{})
}

func (p *DupeLinkPlugin) Start(sharedDb *storm.DB) {
	db = sharedDb
}

func (p *DupeLinkPlugin) Run(message telebot.Message) {
	messageURLs := getURLs(message)
	var validURLs []string
	
	newURL := func(currentURL string, validURLs []string) bool {
		for _, existingURL := range validURLs {
			if existingURL == currentURL {
				return false
			}
		}

		return true
	}

	for _, messageURL := range messageURLs {
		parsedURL, err := url.Parse(messageURL)

		if err != nil {
			continue
		}
	
		currentURL := parsedURL.Hostname() + parsedURL.RequestURI()

		if newURL(currentURL, validURLs) {
			validURLs = append(validURLs, currentURL)
			reactToURL(currentURL, message)
		}
	}
}

func (p *DupeLinkPlugin) Stop() {
}

func getURLs(message telebot.Message) []string {
	var urls []string

	for _, entity := range message.Entities {
		if entity.Type == "url" {
			urls = append(urls, message.Text[entity.Offset:(entity.Offset + entity.Length)])
		}
	}

	return urls
}

func reactToURL(currentURL string, message telebot.Message) {
	chat := db.From(strconv.FormatInt(message.Chat.ID, 10))
	
	var existingLink DupeLink
	err := chat.One("URL", currentURL, &existingLink);

	if err == nil {
		log.Println("Found dupe, reporting: " + currentURL)

		bot := registry.Bot
		formattedTime := time.Unix(int64(existingLink.Unixtime), 0).Format(time.RFC3339)
		formattedUser := existingLink.Sender.FirstName + " " + existingLink.Sender.LastName
		bot.SendMessage(message.Chat, "That was already posted on " + formattedTime + " by " + formattedUser,
						&telebot.SendOptions{ReplyTo: message})
	} else {
		log.Println("Link not found, saving: " + currentURL)

		newLink := DupeLink{URL: currentURL,
							MessageID: message.ID,
							Sender: message.Sender,
							Unixtime: message.Unixtime}
		chat.Save(&newLink)
	}
}
