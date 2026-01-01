package vk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	Token   string
	OwnerID string
	HTTP    *http.Client
}

type WallItem struct {
	ID          int          `json:"id"`
	Text        string       `json:"text"`
	Pinned      int          `json:"is_pinned,omitempty"`
	Ads         int          `json:"marked_as_ads,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Attachment struct {
	Type  string `json:"type"`
	Photo *Photo `json:"photo,omitempty"`
}

type Photo struct {
	ID    int64       `json:"id"`
	Sizes []PhotoSize `json:"sizes,omitempty"`
}

type PhotoSize struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Type   string `json:"type"`
}

type Post struct {
	VKOwnerID string
	VKPostID  string
	VKFullID  string
	Link      string
	Text      string
	MediaURLs []string // <= до 10 ссылок на фото
}

type wallGetResp struct {
	Response struct {
		Count int        `json:"count"`
		Items []WallItem `json:"items"`
	} `json:"response"`
	Error *struct {
		ErrorCode int    `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
	} `json:"error,omitempty"`
}

func New(token, ownerID string) *Client {
	return &Client{
		Token:   token,
		OwnerID: ownerID,
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// FetchWall:
// - limit > 0: тянем максимум limit постов
// - limit <= 0: тянем ВСЕ посты со стены (до конца)
func (c *Client) FetchWall(limit int) ([]WallItem, error) {
	const pageSize = 100 // VK wall.get max per request

	all := make([]WallItem, 0, 512)
	offset := 0
	total := -1

	// пока не кончилось
	for {
		// сколько хотим на этой странице
		want := pageSize
		if limit > 0 {
			remain := limit - len(all)
			if remain <= 0 {
				break
			}
			if remain < want {
				want = remain
			}
		}

		items, cnt, err := c.fetchWallPage(want, offset)
		if err != nil {
			return nil, err
		}
		if total < 0 {
			total = cnt
		}

		if len(items) == 0 {
			break
		}

		all = append(all, items...)
		offset += len(items)

		// дошли до конца стены
		if total >= 0 && offset >= total {
			break
		}

		// чуть-чуть притормозить, чтобы VK не психанул
		time.Sleep(350 * time.Millisecond)
	}

	return all, nil
}

// один запрос wall.get (count <= 100) с offset
func (c *Client) fetchWallPage(count, offset int) ([]WallItem, int, error) {
	if count <= 0 {
		count = 50
	}
	if count > 100 {
		count = 100
	}
	if offset < 0 {
		offset = 0
	}

	u, _ := url.Parse("https://api.vk.com/method/wall.get")
	q := u.Query()
	q.Set("owner_id", c.OwnerID)
	q.Set("count", fmt.Sprintf("%d", count))
	q.Set("offset", fmt.Sprintf("%d", offset))
	q.Set("filter", "owner")
	q.Set("access_token", c.Token)
	q.Set("v", "5.131")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequest("GET", u.String(), nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	var data wallGetResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, 0, err
	}
	if data.Error != nil {
		return nil, 0, fmt.Errorf("vk error %d: %s", data.Error.ErrorCode, data.Error.ErrorMsg)
	}

	return data.Response.Items, data.Response.Count, nil
}

// ExtractPosts: каждый VK-пост -> один Post с альбомом до 10 фоток
func (c *Client) ExtractPosts(items []WallItem) []Post {
	out := make([]Post, 0, len(items))

	for _, it := range items {
		if it.Pinned == 1 || it.Ads == 1 {
			continue
		}

		media := make([]string, 0, 10)
		for _, att := range it.Attachments {
			if att.Type != "photo" || att.Photo == nil {
				continue
			}
			u := bestPhotoURL(att.Photo)
			if u == "" {
				continue
			}
			media = append(media, u)
			if len(media) == 10 {
				break // лимит телеги
			}
		}

		if len(media) == 0 {
			continue
		}

		vkPostID := fmt.Sprintf("%d", it.ID)
		vkFull := fmt.Sprintf("%s_%s", c.OwnerID, vkPostID)
		link := fmt.Sprintf("https://vk.com/wall%s", vkFull)

		out = append(out, Post{
			VKOwnerID: c.OwnerID,
			VKPostID:  vkPostID,
			VKFullID:  vkFull,
			Link:      link,
			Text:      it.Text,
			MediaURLs: media,
		})
	}

	return out
}

func bestPhotoURL(p *Photo) string {
	if p == nil || len(p.Sizes) == 0 {
		return ""
	}
	bestURL := ""
	bestArea := -1
	for _, s := range p.Sizes {
		if s.URL == "" {
			continue
		}
		area := s.Width * s.Height
		if area > bestArea {
			bestArea = area
			bestURL = s.URL
		}
	}
	return bestURL
}
