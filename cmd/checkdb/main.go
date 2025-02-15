package main

import (
	"log"

	"github.com/asdine/storm"
	"github.com/tucnak/telebot"
)

func main() {
	// Open Storm DB
	stormDb, err := storm.Open("../db/muxgoob.db")
	if err != nil {
		log.Fatal("Failed to open Storm DB:", err)
	}
	defer stormDb.Close()

	// List all chat buckets
	log.Println("Available chats in Storm DB:")
	var messages []telebot.Message
	err = stormDb.All(&messages)
	if err != nil {
		log.Printf("Error getting messages: %v", err)
	}

	// Count messages per chat
	chatCounts := make(map[int64]int)
	for _, msg := range messages {
		chatCounts[msg.Chat.ID]++
	}

	for chatID, count := range chatCounts {
		log.Printf("Chat %d: %d messages", chatID, count)
	}

	// Try to get dupe_links info
	var dupeLinks []string
	err = stormDb.From("dupe_links").All(&dupeLinks)
	if err != nil && err != storm.ErrNotFound {
		log.Printf("Error getting dupe_links: %v", err)
	} else {
		log.Printf("Found %d entries in dupe_links bucket", len(dupeLinks))
	}

	// Print some stats
	log.Printf("\nDatabase Statistics:")
	log.Printf("Total messages: %d", len(messages))
	log.Printf("Total chats: %d", len(chatCounts))
	if avgMsgs := float64(len(messages)) / float64(len(chatCounts)); len(chatCounts) > 0 {
		log.Printf("Average messages per chat: %.2f", avgMsgs)
	}
}
