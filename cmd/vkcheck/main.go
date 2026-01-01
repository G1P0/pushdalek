package main

import (
	"fmt"
	"log"
	"os"

	"github.com/G1P0/pushdalek/internal/vk"
)

func main() {
	token := os.Getenv("VK_TOKEN")
	ownerID := os.Getenv("VK_OWNER_ID")

	if token == "" || ownerID == "" {
		log.Fatal("need VK_TOKEN and VK_OWNER_ID in env (source .env first)")
	}

	c := vk.New(token, ownerID)

	items, err := c.FetchWall(20)
	if err != nil {
		log.Fatalf("FetchWall error: %v", err)
	}

	posts := c.ExtractPosts(items)

	fmt.Printf("wall items=%d, posts_with_photos=%d\n", len(items), len(posts))
	if len(posts) == 0 {
		fmt.Println("no photo posts found in first 20 items. try bigger count.")
		return
	}

	p := posts[0]
	fmt.Println("example post:")
	fmt.Println("  vk_full_id:", p.VKFullID)
	fmt.Println("  link:", p.Link)
	fmt.Println("  text_len:", len(p.Text))
	fmt.Println("  photos:", len(p.MediaURLs))
	if len(p.MediaURLs) > 0 {
		fmt.Println("  first_photo_url:", p.MediaURLs[0])
	}
}
