package main

import (
	"fmt"
	"html"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/G1P0/pushdalek/internal/store"
	"github.com/G1P0/pushdalek/internal/vk"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	perPageUsed = 10
)

func main() {
	// --- env ---
	tgToken := mustEnv("TG_BOT_TOKEN")
	vkToken := mustEnv("VK_TOKEN")
	vkOwner := mustEnv("VK_OWNER_ID")

	dbPath := getenvDefault("DB_PATH", "bot.db")

	// админы: TG_ADMIN_IDS=123,456,789
	adminIDs := parseAdminIDs(os.Getenv("TG_ADMIN_IDS"))
	log.Printf("admins loaded: %d", len(adminIDs))

	// тег: ARCHIVE_TAG=#матрица (или ARCHIVE_TAG=матрица)
	archiveTag := normalizeTag(getenvDefault("ARCHIVE_TAG", "#архив"))
	log.Printf("archive tag: %s", archiveTag)

	// --- tg bot ---
	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		log.Fatal(err)
	}
	bot.Debug = false
	log.Printf("Bot started as %s", bot.Self.UserName)

	// --- store ---
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	// --- updates loop ---
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for upd := range updates {
		// callbacks (кнопки)
		if upd.CallbackQuery != nil {
			handleCallback(bot, st, upd.CallbackQuery, adminIDs, vkToken, vkOwner, archiveTag)
			continue
		}

		// обычные сообщения
		if upd.Message == nil {
			continue
		}

		chatID := upd.Message.Chat.ID
		userID := int64(upd.Message.From.ID)

		// /whoami доступна всем
		if upd.Message.IsCommand() && upd.Message.Command() == "whoami" {
			txt := fmt.Sprintf("user_id=%d\nchat_id=%d", userID, chatID)
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, txt))
			continue
		}

		// команды кроме /whoami — только админы
		if upd.Message.IsCommand() && !isAdmin(adminIDs, userID) {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "🚫 Нет доступа"))
			continue
		}

		if !upd.Message.IsCommand() {
			continue
		}

		switch upd.Message.Command() {
		case "start", "help":
			sendMenu(bot, chatID)

		case "sync":
			doSync(bot, st, chatID, vkToken, vkOwner)

		case "next":
			doNext(bot, st, chatID, archiveTag, 1)

		case "next5":
			doNext(bot, st, chatID, archiveTag, 5)

		case "used":
			// /used [page]
			page := 0
			if a := strings.TrimSpace(upd.Message.CommandArguments()); a != "" {
				if n, err := strconv.Atoi(a); err == nil && n >= 0 {
					page = n
				}
			}
			sendUsedPage(bot, st, chatID, 0, page)

		default:
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Не знаю такую команду. Жми Menu или /help"))
		}
	}
}

func handleCallback(bot *tgbotapi.BotAPI, st *store.Store, cq *tgbotapi.CallbackQuery, admins map[int64]struct{}, vkToken, vkOwner, archiveTag string) {
	chatID := cq.Message.Chat.ID
	msgID := cq.Message.MessageID
	userID := int64(cq.From.ID)

	// всегда гасим “крутилку”
	_ = answerCallback(bot, cq.ID, "", false)

	// доступ
	if !isAdmin(admins, userID) {
		_ = answerCallback(bot, cq.ID, "Нет доступа", true)
		return
	}

	data := strings.TrimSpace(cq.Data)
	parts := strings.Split(data, ":")

	switch parts[0] {
	case "menu":
		editMenu(bot, chatID, msgID)

	case "whoami":
		txt := fmt.Sprintf("user_id=%d\nchat_id=%d", userID, chatID)
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, txt))

	case "stats":
		stats, _ := st.Stats()
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, formatStats(stats)))
		sendMenu(bot, chatID)

	case "sync":
		doSync(bot, st, chatID, vkToken, vkOwner)
		sendMenu(bot, chatID)

	case "next":
		// next or next:5
		n := 1
		if len(parts) >= 2 {
			if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
				n = v
			}
		}
		doNext(bot, st, chatID, archiveTag, n)
		sendMenu(bot, chatID)

	case "used":
		// used:<page>
		page := 0
		if len(parts) >= 2 {
			if v, err := strconv.Atoi(parts[1]); err == nil && v >= 0 {
				page = v
			}
		}
		sendUsedPage(bot, st, chatID, msgID, page)

	case "uopen":
		// uopen:<page>:<vkfullid>
		if len(parts) < 3 {
			return
		}
		page := 0
		_ = tryAtoi(parts[1], &page)
		vkFull := parts[2]

		p, err := st.GetByVKFullID(vkFull)
		if err != nil || p == nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Не нашёл этот пост в БД."))
			return
		}
		sendUsedDetails(bot, chatID, msgID, page, p)

	case "set":
		// set:<status>:<vkfullid>:<page>
		if len(parts) < 4 {
			return
		}
		status := parts[1]
		vkFull := parts[2]
		page := 0
		_ = tryAtoi(parts[3], &page)

		if status != "new" && status != "skipped" && status != "used" && status != "reserved" {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, "Статус какой-то странный."))
			return
		}

		if err := st.SetStatus(vkFull, status); err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка БД: %v", err)))
			return
		}

		// после смены статуса возвращаемся к used-странице
		sendUsedPage(bot, st, chatID, msgID, page)

	default:
		sendMenu(bot, chatID)

	}
}

func doSync(bot *tgbotapi.BotAPI, st *store.Store, chatID int64, vkToken, vkOwner string) {
	_, _ = bot.Send(tgbotapi.NewMessage(chatID, "🔄 Синхронизирую с VK..."))

	c := vk.New(vkToken, vkOwner)
	items, err := c.FetchWall(200)
	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка VK: %v", err)))
		return
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
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка БД: %v", err)))
		return
	}

	stats, _ := st.Stats()
	_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Добавлено %d новых.\n%s", ins, formatStats(stats))))
}

func doNext(bot *tgbotapi.BotAPI, st *store.Store, chatID int64, archiveTag string, n int) {
	if n < 1 {
		n = 1
	}
	if n > 10 {
		n = 10
	}

	sent := 0
	for i := 0; i < n; i++ {
		p, err := st.PickAndReserveRandom()
		if err != nil {
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка БД: %v", err)))
			break
		}
		if p == nil {
			break
		}

		caption := buildCaptionHTML(p.Text, p.Link, archiveTag)

		if err := sendAlbum(bot, chatID, p.MediaURLs, caption); err != nil {
			// если не отправилось — возвращаем обратно в new, чтобы не “залипло” в reserved
			_ = st.SetStatus(p.VKFullID, "new")
			_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка отправки (вернул в new): %v", err)))
			break
		}

		_ = st.SetStatus(p.VKFullID, "used")
		sent++
	}

	stats, _ := st.Stats()
	if sent == 0 {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Нечего отправлять.\n"+formatStats(stats)))
		return
	}

	_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ Отправлено: %d\n%s", sent, formatStats(stats))))
}

func sendUsedPage(bot *tgbotapi.BotAPI, st *store.Store, chatID int64, msgID int, page int) {
	if page < 0 {
		page = 0
	}

	total, err := st.CountByStatus("used")
	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка БД: %v", err)))
		return
	}

	maxPage := 0
	if total > 0 {
		maxPage = (total - 1) / perPageUsed
	}
	if page > maxPage {
		page = maxPage
	}

	offset := page * perPageUsed
	items, err := st.ListByStatusPage("used", perPageUsed, offset)
	if err != nil {
		_, _ = bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("Ошибка БД: %v", err)))
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("📜 used: страница %d/%d (всего %d)\n\n", page+1, maxPage+1, total))
	if len(items) == 0 {
		b.WriteString("Пусто.")
	} else {
		for i, p := range items {
			b.WriteString(fmt.Sprintf("%d) %s | photos=%d | %s\n", i+1, p.VKFullID, len(p.MediaURLs), p.Link))
		}
	}

	markup := usedKeyboard(page, maxPage, items)

	if msgID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, msgID, b.String())
		edit.ReplyMarkup = &markup
		_, _ = bot.Send(edit)
	} else {
		msg := tgbotapi.NewMessage(chatID, b.String())
		msg.ReplyMarkup = markup
		_, _ = bot.Send(msg)
	}
}

func sendUsedDetails(bot *tgbotapi.BotAPI, chatID int64, msgID int, page int, p *store.Post) {
	txt := buildDetailsText(p)

	markup := detailsKeyboard(page, p)

	edit := tgbotapi.NewEditMessageText(chatID, msgID, txt)
	edit.ReplyMarkup = &markup
	_, _ = bot.Send(edit)
}

func buildDetailsText(p *store.Post) string {
	used := "—"
	if p.UsedAt > 0 {
		used = time.Unix(p.UsedAt, 0).Format("2006-01-02 15:04:05")
	}
	t := strings.TrimSpace(p.Text)
	if len(t) > 800 {
		t = t[:800] + "…"
	}
	return fmt.Sprintf(
		"🔎 Пост\n\nvk_full_id: %s\nstatus: %s\nphotos: %d\nused_at: %s\nlink: %s\n\ntext:\n%s",
		p.VKFullID, p.Status, len(p.MediaURLs), used, p.Link, t,
	)
}

func usedKeyboard(page, maxPage int, items []store.Post) tgbotapi.InlineKeyboardMarkup {
	// навигация
	prev := tgbotapi.NewInlineKeyboardButtonData("⬅️ Prev", fmt.Sprintf("used:%d", page-1))
	next := tgbotapi.NewInlineKeyboardButtonData("Next ➡️", fmt.Sprintf("used:%d", page+1))
	menu := tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "menu")

	if page <= 0 {
		prev = tgbotapi.NewInlineKeyboardButtonData("·", "noop")
	}
	if page >= maxPage {
		next = tgbotapi.NewInlineKeyboardButtonData("·", "noop")
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		{prev, next, menu},
	}

	// кнопки “детали” на 1..N
	if len(items) > 0 {
		row := []tgbotapi.InlineKeyboardButton{}
		for i, p := range items {
			btn := tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d", i+1), fmt.Sprintf("uopen:%d:%s", page, p.VKFullID))
			row = append(row, btn)
			if len(row) == 5 {
				rows = append(rows, row)
				row = []tgbotapi.InlineKeyboardButton{}
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}

	return tgbotapi.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func detailsKeyboard(page int, p *store.Post) tgbotapi.InlineKeyboardMarkup {
	open := tgbotapi.NewInlineKeyboardButtonURL("🔗 Оригинал", p.Link)
	back := tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", fmt.Sprintf("used:%d", page))

	toNew := tgbotapi.NewInlineKeyboardButtonData("↩️ вернуть в new", fmt.Sprintf("set:new:%s:%d", p.VKFullID, page))
	toSkip := tgbotapi.NewInlineKeyboardButtonData("🚫 skipped", fmt.Sprintf("set:skipped:%s:%d", p.VKFullID, page))

	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(open),
		tgbotapi.NewInlineKeyboardRow(toNew, toSkip),
		tgbotapi.NewInlineKeyboardRow(back),
	)
}

func mainMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Sync VK", "sync"),
			tgbotapi.NewInlineKeyboardButtonData("🎲 Next", "next"),
			tgbotapi.NewInlineKeyboardButtonData("🎲×5", "next:5"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Stats", "stats"),
			tgbotapi.NewInlineKeyboardButtonData("📜 Used", "used:0"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🙋 whoami", "whoami"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "menu"),
		),
	)
}

func sendMenu(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "Панель управления:")
	m := mainMenu()
	msg.ReplyMarkup = m
	_, _ = bot.Send(msg)
}

func editMenu(bot *tgbotapi.BotAPI, chatID int64, msgID int) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, "Панель управления:")
	m := mainMenu()
	edit.ReplyMarkup = &m
	_, _ = bot.Send(edit)
}

func sendAlbum(bot *tgbotapi.BotAPI, chatID int64, photoURLs []string, captionHTML string) error {
	if len(photoURLs) == 0 {
		return fmt.Errorf("no photos")
	}

	// 1 фото -> обычное фото
	if len(photoURLs) == 1 {
		msg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(photoURLs[0]))
		if captionHTML != "" {
			msg.Caption = captionHTML
			msg.ParseMode = "HTML"
		}
		_, err := bot.Send(msg)
		return err
	}

	// 2..10 фото -> media group
	if len(photoURLs) > 10 {
		photoURLs = photoURLs[:10]
	}

	media := make([]interface{}, 0, len(photoURLs))
	for i, u := range photoURLs {
		m := tgbotapi.NewInputMediaPhoto(tgbotapi.FileURL(u))
		if i == 0 && captionHTML != "" {
			m.Caption = captionHTML
			m.ParseMode = "HTML"
		}
		media = append(media, m)
	}

	cfg := tgbotapi.NewMediaGroup(chatID, media)
	_, err := bot.SendMediaGroup(cfg)
	return err
}

func buildCaptionHTML(text, link, archiveTag string) string {
	t := strings.TrimSpace(text)
	if t != "" {
		t = html.EscapeString(t)
		t += "\n\n"
	}
	t += html.EscapeString(archiveTag) + "\n"
	t += fmt.Sprintf(`<a href="%s">Оригинал</a>`, html.EscapeString(link))
	return t
}

func formatStats(m map[string]int) string {
	get := func(k string) int { return m[k] }
	return fmt.Sprintf("Статы: new=%d used=%d reserved=%d skipped=%d",
		get("new"), get("used"), get("reserved"), get("skipped"),
	)
}

func parseAdminIDs(s string) map[int64]struct{} {
	out := map[int64]struct{}{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.ParseInt(part, 10, 64); err == nil && id != 0 {
			out[id] = struct{}{}
		}
	}
	return out
}

func isAdmin(admins map[int64]struct{}, userID int64) bool {
	if len(admins) == 0 {
		return false // если админов не задали — никто не админ
	}
	_, ok := admins[userID]
	return ok
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env %s", k)
	}
	return v
}

func getenvDefault(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
}

func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "#архив"
	}
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	return s
}

func tryAtoi(s string, out *int) error {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return err
	}
	*out = v
	return nil
}

func answerCallback(bot *tgbotapi.BotAPI, callbackID, text string, alert bool) error {
	cfg := tgbotapi.CallbackConfig{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       alert,
	}
	_, err := bot.Request(cfg)
	return err
}
