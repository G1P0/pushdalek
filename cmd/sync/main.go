package main

import (
	"fmt"
	"log"
	"os"

	"github.com/G1P0/pushdalek/internal/store"
	"github.com/G1P0/pushdalek/internal/vk"
)

func main() {
	vkToken := os.Getenv("VK_TOKEN")
	vkOwner := os.Getenv("VK_OWNER_ID")
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "bot.db"
	}

	if vkToken == "" || vkOwner == "" {
		log.Fatal("need VK_TOKEN and VK_OWNER_ID")
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	c := vk.New(vkToken, vkOwner)
	items, err := c.FetchWall(100)
	if err != nil {
		log.Fatal(err)
	}

	parsed := c.ExtractPosts(items)

	posts := make([]store.Post, 0, len(parsed))
	for _, p := range parsed {
		posts = append(posts, store.Post{
			VKOwnerID: p.VKOwnerID,
			VKPostID:  p.VKPostID,
			VKFullID:  p.VKFullID,
			Link:      p.Link,
			Text:      p.Text,
			MediaURLs: p.MediaURLs,
		})
	}

	ins, err := st.UpsertPosts(posts)
	if err != nil {
		log.Fatal(err)
	}

	stats, _ := st.Stats()
	fmt.Printf("sync ok: wall=%d parsed=%d inserted=%d stats=%v db=%s\n",
		len(items), len(posts), ins, stats, dbPath)
}
