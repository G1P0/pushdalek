package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Post struct {
	VKOwnerID string
	VKPostID  string
	VKFullID  string
	Link      string
	Text      string

	MediaURLs []string

	Status    string
	CreatedAt int64
	UpdatedAt int64
	UsedAt    int64
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ensureSchema(ctx context.Context) error {
	// базовая таблица
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS posts (
  vk_full_id  TEXT PRIMARY KEY,
  vk_owner_id TEXT NOT NULL,
  vk_post_id  TEXT NOT NULL,
  link        TEXT NOT NULL,
  text        TEXT NOT NULL,
  media_json  TEXT NOT NULL DEFAULT '[]',
  status      TEXT NOT NULL DEFAULT 'new',
  created_at  INTEGER NOT NULL DEFAULT 0,
  updated_at  INTEGER NOT NULL DEFAULT 0,
  used_at     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_posts_status_usedat    ON posts(status, used_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_status_createdat ON posts(status, created_at DESC);
`)
	if err != nil {
		return err
	}

	// миграции для старых баз
	cols, err := s.tableColumns(ctx, "posts")
	if err != nil {
		return err
	}

	addCol := func(name, ddl string) error {
		if cols[name] {
			return nil
		}
		_, e := s.db.ExecContext(ctx, ddl)
		return e
	}

	if err := addCol("media_json", `ALTER TABLE posts ADD COLUMN media_json TEXT NOT NULL DEFAULT '[]';`); err != nil {
		return err
	}
	if err := addCol("status", `ALTER TABLE posts ADD COLUMN status TEXT NOT NULL DEFAULT 'new';`); err != nil {
		return err
	}
	if err := addCol("created_at", `ALTER TABLE posts ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;`); err != nil {
		return err
	}
	if err := addCol("updated_at", `ALTER TABLE posts ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;`); err != nil {
		return err
	}
	if err := addCol("used_at", `ALTER TABLE posts ADD COLUMN used_at INTEGER NOT NULL DEFAULT 0;`); err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_posts_status_usedat    ON posts(status, used_at DESC);
CREATE INDEX IF NOT EXISTS idx_posts_status_createdat ON posts(status, created_at DESC);
`)
	if err != nil {
		return err
	}

	// “убираем reserved/skipped” как класс:
	// всё что не new/used -> new
	_, err = s.db.ExecContext(ctx, `
UPDATE posts
SET status='new', updated_at=COALESCE(updated_at, 0)
WHERE status NOT IN ('new','used');
`)
	if err != nil {
		return err
	}

	// если вдруг были used без used_at — поставим used_at=updated_at/created_at
	_, _ = s.db.ExecContext(ctx, `
UPDATE posts
SET used_at = CASE
  WHEN used_at=0 AND updated_at>0 THEN updated_at
  WHEN used_at=0 AND created_at>0 THEN created_at
  ELSE used_at
END
WHERE status='used';
`)

	return nil
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s);`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) UpsertPosts(posts []Post) (inserted int, err error) {
	if len(posts) == 0 {
		return 0, nil
	}

	now := time.Now().Unix()

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	insStmt, err := tx.Prepare(`
INSERT OR IGNORE INTO posts
(vk_full_id, vk_owner_id, vk_post_id, link, text, media_json, status, created_at, updated_at, used_at)
VALUES (?, ?, ?, ?, ?, ?, 'new', ?, ?, 0);
`)
	if err != nil {
		return 0, err
	}
	defer insStmt.Close()

	updStmt, err := tx.Prepare(`
UPDATE posts
SET link=?, text=?, media_json=?, updated_at=?
WHERE vk_full_id=?;
`)
	if err != nil {
		return 0, err
	}
	defer updStmt.Close()

	for _, p := range posts {
		mediaJSON, _ := json.Marshal(p.MediaURLs)
		res, e := insStmt.Exec(p.VKFullID, p.VKOwnerID, p.VKPostID, p.Link, p.Text, string(mediaJSON), now, now)
		if e != nil {
			err = e
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted += int(n)
		}

		// обновляем контент (без смены статуса)
		if _, e := updStmt.Exec(p.Link, p.Text, string(mediaJSON), now, p.VKFullID); e != nil {
			err = e
			return 0, err
		}
	}

	err = tx.Commit()
	return inserted, err
}

// Stats: считаем только new/used. Всё остальное уже миграцией превращаем в new.
func (s *Store) Stats() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM posts GROUP BY status;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{
		"new":  0,
		"used": 0,
	}
	for rows.Next() {
		var st string
		var c int
		if err := rows.Scan(&st, &c); err != nil {
			return nil, err
		}
		if st == "new" || st == "used" {
			out[st] = c
		}
	}
	return out, rows.Err()
}

func (s *Store) CountByStatus(status string) (int, error) {
	if status != "new" && status != "used" {
		return 0, fmt.Errorf("unsupported status: %s", status)
	}
	row := s.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE status=?;`, status)
	var n int
	return n, row.Scan(&n)
}

func (s *Store) GetByVKFullID(vkFullID string) (*Post, error) {
	row := s.db.QueryRow(`
SELECT vk_owner_id, vk_post_id, vk_full_id, link, text, media_json, status, created_at, updated_at, used_at
FROM posts
WHERE vk_full_id=?;
`, vkFullID)

	var p Post
	var mediaJSON string
	err := row.Scan(&p.VKOwnerID, &p.VKPostID, &p.VKFullID, &p.Link, &p.Text, &mediaJSON, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.UsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(mediaJSON), &p.MediaURLs)
	// на всякий: если в базе внезапно был старый статус
	if p.Status != "new" && p.Status != "used" {
		p.Status = "new"
	}
	return &p, nil
}

func (s *Store) SetStatus(vkFullID, status string) error {
	if status != "new" && status != "used" {
		return fmt.Errorf("unsupported status: %s", status)
	}
	now := time.Now().Unix()
	usedAt := int64(0)
	if status == "used" {
		usedAt = now
	}
	_, err := s.db.Exec(`
UPDATE posts
SET status=?, updated_at=?, used_at=?
WHERE vk_full_id=?;
`, status, now, usedAt, vkFullID)
	return err
}

// PickRandomNew: выбираем случайный new
func (s *Store) PickRandomNew() (*Post, error) {
	row := s.db.QueryRow(`
SELECT vk_owner_id, vk_post_id, vk_full_id, link, text, media_json, status, created_at, updated_at, used_at
FROM posts
WHERE status='new'
ORDER BY RANDOM()
LIMIT 1;
`)

	var p Post
	var mediaJSON string
	if err := row.Scan(&p.VKOwnerID, &p.VKPostID, &p.VKFullID, &p.Link, &p.Text, &mediaJSON, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.UsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(mediaJSON), &p.MediaURLs)
	return &p, nil
}

func (s *Store) ListByStatusPage(status string, limit, offset int) ([]Post, error) {
	if status != "new" && status != "used" {
		return nil, fmt.Errorf("unsupported status: %s", status)
	}
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	order := "created_at DESC"
	if status == "used" {
		order = "used_at DESC"
	}

	rows, err := s.db.Query(fmt.Sprintf(`
SELECT vk_owner_id, vk_post_id, vk_full_id, link, text, media_json, status, created_at, updated_at, used_at
FROM posts
WHERE status=?
ORDER BY %s
LIMIT ? OFFSET ?;
`, order), status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Post{}
	for rows.Next() {
		var p Post
		var mediaJSON string
		if err := rows.Scan(&p.VKOwnerID, &p.VKPostID, &p.VKFullID, &p.Link, &p.Text, &mediaJSON, &p.Status, &p.CreatedAt, &p.UpdatedAt, &p.UsedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(mediaJSON), &p.MediaURLs)
		out = append(out, p)
	}
	return out, rows.Err()
}
