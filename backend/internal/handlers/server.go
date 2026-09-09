package handlers

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tribuna-portal/internal/data"
	"tribuna-portal/internal/model"
)

type Config struct{ DataDir, FrontendDir, TemplateDir, AdminURL, ContactEmail string }
type Page struct {
	Bundle                                                                    model.Bundle
	Items                                                                     []model.Article
	Article                                                                   *model.Article
	Category, Query, Title, Mode, PrevURL, NextURL, ContactEmail, RequestPath string
	Page, Total                                                               int
	HasNext                                                                   bool
	Initial                                                                   template.JS
	Body                                                                      template.HTML
}
type Site struct {
	store     *data.Store
	cfg       Config
	templates *template.Template
	mu        sync.Mutex
	cached    *model.Bundle
	sse       atomic.Int32
}

func New(cfg Config) (*Site, error) {
	funcs := template.FuncMap{
		"tag": Tag, "date": Date, "time": Clock, "articleURL": ArticleURL, "art": ArticleArt,
		"add": func(a, b int) int { return a + b }, "sub": func(a, b int) int { return a - b },
		"lower": strings.ToLower, "year": func() int { return time.Now().Year() },
	}
	tmpl, err := template.New("site").Funcs(funcs).ParseGlob(filepath.Join(cfg.TemplateDir, "*.html"))
	if err != nil {
		return nil, err
	}
	st, err := data.Open(filepath.Join(cfg.DataDir, "ingester.db"))
	if err != nil {
		return nil, err
	}
	return &Site{cfg: cfg, templates: tmpl, store: st}, nil
}
func (s *Site) Close() error { return s.store.Close() }
func Tag(s string) string {
	for _, n := range model.Navigation() {
		if n.ID == s {
			return n.Label
		}
	}
	return "Спорт"
}
func Date(s string) string {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		return ""
	}
	return t.In(time.FixedZone("MSK", 3*3600)).Format("02.01.2006")
}
func Clock(s string) string {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		return ""
	}
	return t.In(time.FixedZone("MSK", 3*3600)).Format("15:04")
}
func ArticleArt(tag string) string {
	switch tag {
	case "football", "hockey", "tennis", "basketball", "motorsport":
		return "/assets/" + tag + ".svg"
	}
	return "/assets/football.svg"
}
func ArticleURL(a model.Article) string { return "/news/" + url.PathEscape(a.ID) }
func safeURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return raw
}
func safeImage(raw string) string {
	if strings.HasPrefix(raw, "/assets/") || strings.HasPrefix(raw, "/uploads/") {
		if strings.Contains(raw, "..") || strings.ContainsAny(raw, "\\\r\n") {
			return ""
		}
		return raw
	}
	return safeURL(raw)
}
func cleanBundle(b model.Bundle) model.Bundle {
	b.Feed = slices.Clone(b.Feed)
	b.Stories = slices.Clone(b.Stories)
	b.Ads = slices.Clone(b.Ads)
	b.Sources = slices.Clone(b.Sources)
	if b.Feed == nil {
		b.Feed = []model.Article{}
	}
	if b.Stories == nil {
		b.Stories = []model.Article{}
	}
	if b.Ads == nil {
		b.Ads = []model.Ad{}
	}
	if b.Sources == nil {
		b.Sources = []model.SourceStatus{}
	}
	if b.Scoreboard == nil {
		b.Scoreboard = []any{}
	}
	if b.Opinions == nil {
		b.Opinions = []any{}
	}

	clean := func(a model.Article) model.Article {
		a.URL = safeURL(a.URL)
		a.ImagePath = safeImage(a.ImagePath)
		return a
	}
	for i := range b.Feed {
		b.Feed[i] = clean(b.Feed[i])
	}
	for i := range b.Stories {
		b.Stories[i] = clean(b.Stories[i])
	}
	if b.Hero != nil {
		a := clean(*b.Hero)
		b.Hero = &a
	}
	for i := range b.Ads {
		b.Ads[i].ClickURL = safeURL(b.Ads[i].ClickURL)
		b.Ads[i].File = safeImage(b.Ads[i].File)
	}
	if len(b.Nav) == 0 {
		b.Nav = model.Navigation()
	}
	return b
}
func (s *Site) bundle() (model.Bundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{"bundle.json", "bundle.fixture.json"} {
		if name == "bundle.fixture.json" && s.cached != nil && !s.cached.IsDemo {
			return s.enrich(*s.cached), nil
		}
		p := filepath.Join(s.cfg.DataDir, name)
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var b model.Bundle
		err = json.NewDecoder(io.LimitReader(f, 16<<20)).Decode(&b)
		f.Close()
		if err != nil {
			continue
		}
		if len(b.Feed) == 0 {
			continue
		}
		b = cleanBundle(b)
		if name == "bundle.fixture.json" {
			b.IsDemo = true
			for i := range b.Feed {
				b.Feed[i].IsDemo = true
			}
			if b.Hero != nil {
				b.Hero.IsDemo = true
			}
		}
		s.cached = &b
		return s.enrich(b), nil
	}
	if s.cached != nil {
		return s.enrich(*s.cached), nil
	}
	return model.Bundle{Nav: model.Navigation(), Feed: []model.Article{}, Ads: []model.Ad{}, Sources: []model.SourceStatus{}}, errors.New("no readable content bundle")
}
func (s *Site) enrich(b model.Bundle) model.Bundle {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if ads, e := s.store.ActiveAds(ctx); e == nil {
		b.Ads = ads
	}
	if sources, e := s.store.SourceStatuses(ctx); e == nil && len(sources) > 0 {
		b.Sources = sources
	}
	return cleanBundle(b)
}
func (s *Site) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/bundle", s.apiBundle)
	mux.HandleFunc("GET /api/v1/news", s.apiNews)
	mux.HandleFunc("GET /api/v1/feed/stream", s.stream)
	mux.HandleFunc("GET /feed.xml", s.rss)
	mux.HandleFunc("GET /uploads/", s.file)
	mux.HandleFunc("GET /assets/", s.file)
	mux.HandleFunc("GET /styles.css", s.file)
	mux.HandleFunc("GET /style.css", s.file)
	mux.HandleFunc("GET /v2-broadsheet.js", s.file)
	mux.HandleFunc("GET /bookmakers-widget.js", s.file)
	mux.HandleFunc("GET /bookmakers-widget.css", s.file)
	mux.HandleFunc("GET /bookmakers.json", s.file)
	mux.HandleFunc("GET /favicon.svg", s.file)
	mux.HandleFunc("GET /favicon.ico", s.file)
	if s.cfg.AdminURL != "" {
		u, e := url.Parse(s.cfg.AdminURL)
		if e == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") {
			proxy := httputil.NewSingleHostReverseProxy(u)
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, "Сервис управления временно недоступен", http.StatusBadGateway)
			}
			mux.Handle("/admin/", proxy)
			mux.Handle("/admin", proxy)
		}
	}
	mux.HandleFunc("/", s.page)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; font-src 'self'; connect-src 'self'; frame-src 'none'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}
func (s *Site) health(w http.ResponseWriter, r *http.Request) {
	b, e := s.bundle()
	state := "ok"
	if e != nil {
		state = "empty"
	}
	writeJSON(w, map[string]any{"status": state, "updated_at": b.UpdatedAt, "demo": b.IsDemo, "items": len(b.Feed)})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
func (s *Site) apiBundle(w http.ResponseWriter, r *http.Request) { b, _ := s.bundle(); writeJSON(w, b) }
func selectItems(b model.Bundle, r *http.Request) ([]model.Article, int, int) {
	category := r.URL.Query().Get("category")
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	result := []model.Article{}
	for _, a := range b.Feed {
		if category != "" && category != "all" && a.Tag != category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(a.Title+" "+a.Summary+" "+a.SourceName), q) {
			continue
		}
		result = append(result, a)
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 48 {
		limit = 12
	}
	return result, page, limit
}
func paginate(items []model.Article, page, limit int) []model.Article {
	start := (page - 1) * limit
	if start >= len(items) {
		return []model.Article{}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}
func (s *Site) apiNews(w http.ResponseWriter, r *http.Request) {
	b, _ := s.bundle()
	items, p, l := selectItems(b, r)
	writeJSON(w, map[string]any{"items": paginate(items, p, l), "total": len(items), "page": p, "has_next": p*l < len(items), "is_demo": b.IsDemo})
}
func pageURL(r *http.Request, p int) string {
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(p))
	return "/?" + q.Encode() + "#feed"
}
func (s *Site) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", 405)
		return
	}
	b, _ := s.bundle()
	items, p, l := selectItems(b, r)
	page := Page{Bundle: b, Items: paginate(items, p, l), Category: r.URL.Query().Get("category"), Query: r.URL.Query().Get("q"), Title: "Трибуна.ру — спорт с характером", Mode: "home", Page: p, Total: len(items), HasNext: p*l < len(items), PrevURL: pageURL(r, p-1), NextURL: pageURL(r, p+1), ContactEmail: s.cfg.ContactEmail, RequestPath: r.URL.Path}
	if p == 1 {
		page.PrevURL = ""
	}
	if !page.HasNext {
		page.NextURL = ""
	}
	status := http.StatusOK
	switch r.URL.Path {
	case "/":
		if page.Category != "" && page.Category != "all" {
			page.Title = Tag(page.Category) + " — Трибуна.ру"
		}
		if page.Query != "" {
			page.Title = "Поиск: " + page.Query + " — Трибуна.ру"
		}
	case "/about", "/sources", "/advertise", "/privacy", "/bookmarks":
		page.Mode = strings.TrimPrefix(r.URL.Path, "/")
		if page.Mode == "bookmarks" {
			page.Items = b.Feed
		}
		names := map[string]string{"about": "О проекте", "sources": "Источники новостей", "advertise": "Рекламодателям", "privacy": "Конфиденциальность", "bookmarks": "Сохранённое"}
		page.Title = names[page.Mode] + " — Трибуна.ру"
	default:
		if strings.HasPrefix(r.URL.Path, "/news/") {
			id := strings.TrimPrefix(r.URL.Path, "/news/")
			for _, a := range b.Feed {
				if a.ID == id {
					copy := a
					page.Article = &copy
					break
				}
			}
			if page.Article == nil {
				if a, e := s.store.GetItem(r.Context(), id); e == nil {
					a.URL = safeURL(a.URL)
					a.ImagePath = safeImage(a.ImagePath)
					page.Article = &a
				}
			}
			if page.Article != nil {
				page.Mode = "article"
				page.Title = page.Article.Title + " — Трибуна.ру"
			} else {
				status = 404
				page.Mode = "notfound"
				page.Title = "Материал не найден — Трибуна.ру"
			}
		} else {
			status = 404
			page.Mode = "notfound"
			page.Title = "Страница не найдена — Трибуна.ру"
		}
	}
	payload, _ := json.Marshal(b)
	page.Initial = template.JS(payload)
	var out strings.Builder
	if err := s.templates.ExecuteTemplate(&out, "index.html", page); err != nil {
		log.Printf("template error: %v", err)
		http.Error(w, "Ошибка отображения страницы", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	io.WriteString(w, out.String())
}
func (s *Site) file(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Contains(name, "..") || strings.Contains(name, "\\") {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	allowed := map[string]bool{".css": true, ".js": true, ".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".ico": true, ".woff2": true, ".woff": true}
	if (!allowed[ext] && name != "bookmakers.json") || (strings.HasPrefix(name, "uploads/") && (ext == ".svg" || ext == ".js" || ext == ".css")) {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.cfg.FrontendDir, filepath.FromSlash(name))
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	if name == "bookmakers.json" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, p)
}
func (s *Site) stream(w http.ResponseWriter, r *http.Request) {
	if s.sse.Add(1) > 100 {
		s.sse.Add(-1)
		http.Error(w, "Too many streams", 503)
		return
	}
	defer s.sse.Add(-1)
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	last := ""
	for {
		b, _ := s.bundle()
		rev := b.UpdatedAt
		if rev != last {
			payload, _ := json.Marshal(b)
			fmt.Fprintf(w, "event: bundle\ndata: %s\n\n", payload)
			last = rev
		} else {
			fmt.Fprint(w, ": keepalive\n\n")
		}
		f.Flush()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Site) rss(w http.ResponseWriter, r *http.Request) {
	type Item struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Date        string `xml:"pubDate"`
		GUID        string `xml:"guid"`
	}
	type Channel struct {
		Title       string `xml:"title"`
		Description string `xml:"description"`
		Items       []Item `xml:"item"`
	}
	b, _ := s.bundle()
	ch := Channel{Title: "Трибуна.ру", Description: "Спортивные новости: заголовки и ссылки на источники"}
	for _, a := range b.Feed {
		if a.URL == "" {
			continue
		}
		d := a.PublishedAt
		if t, e := time.Parse(time.RFC3339, d); e == nil {
			d = t.Format(time.RFC1123Z)
		}
		ch.Items = append(ch.Items, Item{a.Title, a.URL, a.Summary, d, a.ID})
		if len(ch.Items) >= 40 {
			break
		}
	}
	doc := struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel Channel  `xml:"channel"`
	}{Version: "2.0", Channel: ch}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	io.WriteString(w, xml.Header)
	xml.NewEncoder(w).Encode(doc)
}
