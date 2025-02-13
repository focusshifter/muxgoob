package main

import (
	"log"

	"github.com/asdine/storm"
)

func main() {
	// Open Storm DB
	stormDb, err := storm.Open("../db/muxgoob.db")
	if err != nil {
		log.Fatal("Failed to open Storm DB:", err)
	}
	defer stormDb.Close()

	// List all buckets
	buckets := stormDb.Bucket()
	log.Println("Available buckets in Storm DB:")
	for _, b := range buckets {
		log.Printf("- %s", b)
	}

	// Try to get all buckets
	var allBuckets []string
	err = stormDb.AllBuckets(&allBuckets)
	if err != nil {
		log.Printf("Error getting all buckets: %v", err)
	} else {
		log.Printf("All buckets: %v", allBuckets)
	}

	// Try to get dupe_links bucket info
	var dupeLinksInfo []string
	err = stormDb.From("dupe_links").AllKeys(&dupeLinksInfo)
	if err != nil {
		log.Printf("Error getting dupe_links keys: %v", err)
	} else {
		log.Printf("Found %d keys in dupe_links bucket", len(dupeLinksInfo))
	}
}
