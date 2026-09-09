package ingester

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/mmcdole/gofeed"
	xhtml "golang.org/x/net/html"
	"tribuna-portal/internal/data"
	"tribuna-portal/internal/model"
	"tribuna-portal/internal/netguard"
)

type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Tag     string `json:"tag"`
	Enabled bool   `json:"enabled"`
}
type Config struct {
	SourcesPath, DataDir string
	Interval             time.Duration
	FetchImages          bool
	ImagesDir            string
	ValidatePublic       bool
	Client               *http.Client
}
type Ingester struct {
	Store        *data.Store
	Config       Config
	Sources      []Source
	Status       []model.SourceStatus
	etag         map[string]string
	lastModified map[string]string
}

func LoadSources(path string) ([]Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s []Source
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return s, nil
}
func New(st *data.Store, c Config) (*Ingester, error) {
	if c.Interval <= 0 {
		c.Interval = time.Minute
	}
	if c.Client == nil {
		c.Client = netguard.Client(20 * time.Second)
	}
	s, err := LoadSources(c.SourcesPath)
	if err != nil {
		return nil, err
	}
	return &Ingester{Store: st, Config: c, Sources: s, etag: make(map[string]string), lastModified: make(map[string]string)}, nil
}
func (i *Ingester) RunOnce(ctx context.Context) error {
	var good int
	var errs []string
	statuses := make([]model.SourceStatus, 0, len(i.Sources))
	previous, _ := i.Store.SourceStatuses(ctx)
	prev := make(map[string]model.SourceStatus, len(previous))
	for _, p := range previous {
		prev[p.ID] = p
	}
	for _, s := range i.Sources {
		if !s.Enabled {
			continue
		}
		st := model.SourceStatus{ID: s.ID, Name: s.Name, URL: s.URL}
		if p, ok := prev[s.ID]; ok {
			st.LastSuccess = p.LastSuccess
			st.ItemCount = p.ItemCount
		}
		items, err := i.fetchRetry(ctx, s)
		if err != nil {
			st.Status = "error"
			st.LastError = err.Error()
			errs = append(errs, s.ID+": "+err.Error())
		} else {
			if err = i.Store.UpsertItems(ctx, items); err != nil {
				st.Status = "error"
				st.LastError = err.Error()
				errs = append(errs, s.ID+": "+err.Error())
			} else {
				good++
				st.Status = "ok"
				st.LastSuccess = time.Now().UTC().Format(time.RFC3339)
				st.ItemCount = len(items)
			}
		}
		statuses = append(statuses, st)
	}
	i.Status = statuses
	i.Store.SetSourceStatuses(statuses)
	if good == 0 && len(statuses) > 0 {
		return fmt.Errorf("all enabled sources failed: %s", strings.Join(errs, "; "))
	}
	if err := i.Store.WriteBundle(ctx, i.Config.DataDir); err != nil {
		return err
	}
	if i.Config.FetchImages {
		_ = i.CleanupImages(ctx, 30*24*time.Hour)
	}
	return nil
}
func (i *Ingester) Run(ctx context.Context) error {
	ticker := time.NewTicker(i.Config.Interval)
	defer ticker.Stop()
	if err := i.RunOnce(ctx); err != nil && len(i.Sources) == 0 {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := i.RunOnce(ctx); err != nil {
				log.Printf("ingest cycle: %v", err)
			}
		}
	}
}
func (i *Ingester) fetchRetry(ctx context.Context, s Source) ([]model.Article, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		items, err := i.fetch(ctx, s)
		if err == nil {
			return items, nil
		}
		last = err
		if attempt < 2 {
			d := time.Duration(1<<attempt) * 250 * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d):
			}
		}
	}
	return nil, last
}

func (i *Ingester) fetch(ctx context.Context, s Source) ([]model.Article, error) {
	if i.Config.ValidatePublic {
		if err := netguard.ValidateURL(s.URL); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "TribunaNews/1.0 (+RSS reader)")
	if v := i.etag[s.ID]; v != "" {
		req.Header.Set("If-None-Match", v)
	}
	if v := i.lastModified[s.ID]; v != "" {
		req.Header.Set("If-Modified-Since", v)
	}
	resp, err := i.Config.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return []model.Article{}, nil
	}
	if v := resp.Header.Get("ETag"); v != "" {
		i.etag[s.ID] = v
	}
	if v := resp.Header.Get("Last-Modified"); v != "" {
		i.lastModified[s.ID] = v
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	feed, err := new(gofeed.Parser).ParseString(string(body))
	if err != nil {
		return nil, err
	}
	out := make([]model.Article, 0, len(feed.Items))
	for n, it := range feed.Items {
		if n >= 500 {
			break
		}
		if strings.TrimSpace(it.Title) == "" || strings.TrimSpace(it.Link) == "" || !validLink(it.Link) {
			continue
		}
		var published time.Time
		if it.PublishedParsed != nil {
			published = it.PublishedParsed.UTC()
		} else if it.UpdatedParsed != nil {
			published = it.UpdatedParsed.UTC()
		}
		summary := plain(it.Description)
		imagePath := ""
		if i.Config.FetchImages {
			if imageURL := itemImageURL(it); imageURL != "" {
				if p, e := i.downloadImage(ctx, imageURL); e == nil {
					imagePath = p
				}
			}
		}
		if summary == "" {
			summary = plain(it.Content)
		}
		r := []rune(summary)
		if len(r) > 500 {
			summary = string(r[:500]) + "…"
		}
		out = append(out, model.Article{ID: stableID(it.Link), Title: strings.TrimSpace(it.Title), Summary: summary, URL: it.Link, SourceID: s.ID, SourceName: s.Name, Tag: normalizeTag(s.Tag, it.Title, it.Categories, it.Link), ImagePath: imagePath, PublishedAt: func() string {
			if published.IsZero() {
				return ""
			}
			return published.Format(time.RFC3339)
		}(), IsDemo: false})
	}
	return out, nil
}

func plain(v string) string {
	z := xhtml.NewTokenizer(strings.NewReader(v))
	var parts []string
	skip := 0
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		tok := z.Token()
		switch tt {
		case xhtml.StartTagToken:
			if tok.Data == "script" || tok.Data == "style" {
				skip++
			}
		case xhtml.EndTagToken:
			if (tok.Data == "script" || tok.Data == "style") && skip > 0 {
				skip--
			}
		case xhtml.TextToken:
			if skip == 0 {
				parts = append(parts, tok.Data)
			}
		}
	}
	return strings.Join(strings.Fields(html.UnescapeString(strings.Join(parts, " "))), " ")
}

func validLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

func itemImageURL(it *gofeed.Item) string {
	if it.Image != nil && it.Image.URL != "" {
		return it.Image.URL
	}
	for _, e := range it.Enclosures {
		if strings.HasPrefix(strings.ToLower(e.Type), "image/") && e.URL != "" {
			return e.URL
		}
	}
	return ""
}
func (i *Ingester) downloadImage(ctx context.Context, raw string) (string, error) {
	if i.Config.ValidatePublic {
		if err := netguard.ValidateURL(raw); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	resp, err := i.Config.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("image status %s", resp.Status)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20+1))
	if err != nil {
		return "", err
	}
	if len(buf) > 5<<20 {
		return "", fmt.Errorf("image too large")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	if cfg.Width*cfg.Height > 20_000_000 {
		return "", fmt.Errorf("image dimensions too large")
	}
	src, err := imaging.Decode(bytes.NewReader(buf), imaging.AutoOrientation(true))
	if err != nil {
		return "", err
	}
	if src.Bounds().Dx() > 1280 {
		src = imaging.Resize(src, 1280, 0, imaging.Lanczos)
	}
	if i.Config.ImagesDir == "" {
		return "", fmt.Errorf("images dir unset")
	}
	if err = os.MkdirAll(i.Config.ImagesDir, 0755); err != nil {
		return "", err
	}
	h := sha256.Sum256(buf)
	name := hex.EncodeToString(h[:]) + ".jpg"
	path := filepath.Join(i.Config.ImagesDir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(path)
		}
	}()
	err = jpeg.Encode(f, src, &jpeg.Options{Quality: 84})
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return "", err
	}
	ok = true
	return "/uploads/news/" + name, nil
}

func normalizeTag(t, title string, categories []string, link string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	valid := map[string]bool{"football": true, "hockey": true, "tennis": true, "basketball": true, "motorsport": true}
	if valid[t] {
		return t
	}
	keys := []struct {
		tag   string
		words []string
	}{{"football", []string{"футбол", "football", "премьер-лиг", "лига чемпионов", "рпл"}}, {"hockey", []string{"хоккей", "hockey", "кхл", "нхл"}}, {"tennis", []string{"теннис", "tennis", "уимблдон", "ролан гаррос"}}, {"basketball", []string{"баскетбол", "basketball", "нба", "евролига"}}, {"motorsport", []string{"формула-1", "formula-1", "formula1", "моторспорт", "ралли", "motorsport"}}}
	for _, text := range []string{link, strings.Join(categories, " "), title} {
		v := strings.ToLower(text)
		for _, k := range keys {
			for _, word := range k.words {
				if strings.Contains(v, word) {
					return k.tag
				}
			}
		}
	}
	return "other"
}
func stableID(url string) string {
	h := sha256.Sum256([]byte(url))
	return "n-" + hex.EncodeToString(h[:])[:24]
}

// CleanupImages removes only old, unreferenced generated JPEGs; referenced article media is retained.
func (i *Ingester) CleanupImages(ctx context.Context, age time.Duration) error {
	if i.Config.ImagesDir == "" {
		return nil
	}
	rows, err := i.Store.DB.QueryContext(ctx, `SELECT image_path FROM items WHERE image_path<>''`)
	if err != nil {
		return err
	}
	refs := map[string]bool{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		refs[filepath.Base(p)] = true
	}
	if err = rows.Close(); err != nil {
		return err
	}
	entries, err := os.ReadDir(i.Config.ImagesDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-age)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || refs[name] || len(name) != 68 || !strings.HasSuffix(name, ".jpg") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(name, ".jpg")); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err = os.Remove(filepath.Join(i.Config.ImagesDir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}
