package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"tribuna-portal/internal/model"
)

type Store struct {
	DB      *sql.DB
	path    string
	sources []model.SourceStatus
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	schema := `CREATE TABLE IF NOT EXISTS items (id TEXT PRIMARY KEY,url TEXT UNIQUE,title TEXT NOT NULL,summary TEXT,source_id TEXT,source_name TEXT,tag TEXT,image_path TEXT,published_at TEXT,is_demo INTEGER DEFAULT 0,created_at TEXT);
 CREATE INDEX IF NOT EXISTS idx_items_published ON items(published_at DESC);
 CREATE TABLE IF NOT EXISTS source_status (id TEXT PRIMARY KEY,name TEXT,url TEXT,status TEXT,last_success TEXT,last_error TEXT,item_count INTEGER DEFAULT 0);
 CREATE TABLE IF NOT EXISTS ad_creatives (id TEXT PRIMARY KEY,placement TEXT,title TEXT,advertiser TEXT,erid TEXT,click_url TEXT,file TEXT,width INTEGER,height INTEGER,is_active INTEGER DEFAULT 1,starts_at TEXT,ends_at TEXT,created_at TEXT);`
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, path: path}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) UpsertItems(ctx context.Context, items []model.Article) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := `INSERT INTO items(id,url,title,summary,source_id,source_name,tag,image_path,published_at,is_demo,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET title=excluded.title,summary=excluded.summary,source_id=excluded.source_id,source_name=excluded.source_name,tag=excluded.tag,image_path=CASE WHEN excluded.image_path<>'' THEN excluded.image_path ELSE items.image_path END,published_at=CASE WHEN excluded.published_at<>'' THEN excluded.published_at ELSE items.published_at END`
	for _, a := range items {
		if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.Title) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, q, a.ID, a.URL, a.Title, a.Summary, a.SourceID, a.SourceName, a.Tag, a.ImagePath, a.PublishedAt, boolInt(a.IsDemo), time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (s *Store) ListItems(ctx context.Context, limit int) ([]model.Article, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,title,summary,url,source_id,source_name,tag,image_path,published_at,is_demo FROM items ORDER BY published_at DESC,created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Article
	for rows.Next() {
		var a model.Article
		var d int
		if err := rows.Scan(&a.ID, &a.Title, &a.Summary, &a.URL, &a.SourceID, &a.SourceName, &a.Tag, &a.ImagePath, &a.PublishedAt, &d); err != nil {
			return nil, err
		}
		a.IsDemo = d != 0
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) ActiveAds(ctx context.Context) ([]model.Ad, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.DB.QueryContext(ctx, `SELECT id,placement,title,advertiser,erid,click_url,file,width,height FROM ad_creatives WHERE is_active=1 AND (starts_at='' OR starts_at IS NULL OR starts_at<=?) AND (ends_at='' OR ends_at IS NULL OR ends_at>=?) ORDER BY created_at DESC`, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Ad
	for rows.Next() {
		var a model.Ad
		if err := rows.Scan(&a.ID, &a.Placement, &a.Title, &a.Advertiser, &a.ERID, &a.ClickURL, &a.File, &a.Width, &a.Height); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) SetSourceStatuses(v []model.SourceStatus) {
	s.sources = append([]model.SourceStatus(nil), v...)
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, st := range v {
		_, _ = tx.Exec(`INSERT INTO source_status(id,name,url,status,last_success,last_error,item_count) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,status=excluded.status,last_success=CASE WHEN excluded.last_success<>'' THEN excluded.last_success ELSE source_status.last_success END,last_error=excluded.last_error,item_count=excluded.item_count`, st.ID, st.Name, st.URL, st.Status, st.LastSuccess, st.LastError, st.ItemCount)
	}
	_ = tx.Commit()
}
func (s *Store) sourceStatuses(ctx context.Context) []model.SourceStatus {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,url,status,last_success,last_error,item_count FROM source_status ORDER BY id`)
	if err != nil {
		return append([]model.SourceStatus(nil), s.sources...)
	}
	defer rows.Close()
	var out []model.SourceStatus
	for rows.Next() {
		var st model.SourceStatus
		if rows.Scan(&st.ID, &st.Name, &st.URL, &st.Status, &st.LastSuccess, &st.LastError, &st.ItemCount) == nil {
			out = append(out, st)
		}
	}
	if len(out) == 0 {
		return append([]model.SourceStatus(nil), s.sources...)
	}
	return out
}

func (s *Store) BuildBundle(ctx context.Context) (model.Bundle, error) {
	items, err := s.ListItems(ctx, 200)
	if err != nil {
		return model.Bundle{}, err
	}
	ads, err := s.ActiveAds(ctx)
	if err != nil {
		return model.Bundle{}, err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishedAt > items[j].PublishedAt })
	b := model.Bundle{Issue: model.Issue{Number: time.Now().UTC().Format("20060102"), Date: time.Now().UTC().Format("2006-01-02"), Title: "Трибуна"}, Nav: model.Navigation(), Feed: items, Ads: ads, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Sources: s.sourceStatuses(ctx)}
	if len(items) > 0 {
		b.Hero = &items[0]
		if len(items) > 1 {
			b.Stories = items[1:min(6, len(items))]
		}
	}
	return b, nil
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (s *Store) WriteBundle(ctx context.Context, dir string) error {
	b, err := s.BuildBundle(ctx)
	if err != nil {
		return err
	}
	return WriteJSONAtomic(filepath.Join(dir, "bundle.json"), b)
}
func WriteJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bundle-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(buf, '\n')); err == nil {
		err = tmp.Sync()
	}
	if e := tmp.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return fmt.Errorf("replace bundle: %w", err)
	}
	return nil
}

func (s *Store) GetItem(ctx context.Context, id string) (model.Article, error) {
	var a model.Article
	var d int
	err := s.DB.QueryRowContext(ctx, `SELECT id,title,summary,url,source_id,source_name,tag,image_path,published_at,is_demo FROM items WHERE id=?`, id).Scan(&a.ID, &a.Title, &a.Summary, &a.URL, &a.SourceID, &a.SourceName, &a.Tag, &a.ImagePath, &a.PublishedAt, &d)
	a.IsDemo = d != 0
	return a, err
}

func (s *Store) SourceStatuses(ctx context.Context) ([]model.SourceStatus, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,url,status,last_success,last_error,item_count FROM source_status ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SourceStatus
	for rows.Next() {
		var st model.SourceStatus
		if err := rows.Scan(&st.ID, &st.Name, &st.URL, &st.Status, &st.LastSuccess, &st.LastError, &st.ItemCount); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
