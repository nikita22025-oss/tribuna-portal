package handlers

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"tribuna-portal/internal/model"
)

func backendDir(t *testing.T) string {
	t.Helper()
	_, f, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(f), "../.."))
}
func putBundle(t *testing.T, dir, name string, b model.Bundle) {
	t.Helper()
	p, e := json.Marshal(b)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, name), p, 0600); e != nil {
		t.Fatal(e)
	}
}
func makeSite(t *testing.T, dir string) *Site {
	t.Helper()
	root := backendDir(t)
	s, e := New(Config{DataDir: dir, TemplateDir: filepath.Join(root, "templates"), FrontendDir: filepath.Join(root, "../frontend")})
	if e != nil {
		t.Fatalf("production templates: %v", e)
	}
	return s
}
func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}
func sampleBundle() model.Bundle {
	items := []model.Article{
		{ID: "hero", Title: "Футбольная тактика", Summary: "Командная игра", Tag: "football", SourceName: "Источник", URL: "https://example.com/football", PublishedAt: "2024-01-01T12:00:00Z"},
		{ID: "hockey", Title: "Хоккейная смена", Summary: "Лёд и скорость", Tag: "hockey", SourceName: "Источник", URL: "https://example.com/hockey", PublishedAt: "2024-01-01T11:00:00Z"},
		{ID: "tennis", Title: "Теннис и движение", Summary: "Игра у сетки", Tag: "tennis", SourceName: "Источник", URL: "https://example.com/tennis", PublishedAt: "2024-01-01T10:00:00Z"},
	}
	return model.Bundle{Issue: model.Issue{Number: "1", Date: "2024-01-01"}, Nav: model.Navigation(), Hero: &items[0], Feed: items, UpdatedAt: "2024-01-01T12:00:00Z"}
}
func TestPublicFiltersAndArticleStatus(t *testing.T) {
	dir := t.TempDir()
	b := sampleBundle()
	putBundle(t, dir, "bundle.json", b)
	h := makeSite(t, dir).Handler()
	for _, tc := range []struct{ name, target, want, absent string }{
		{"category", "/?category=hockey", "Хоккейная смена", "Футбольная тактика"}, {"query", "/?q=%D1%82%D0%B5%D0%BD%D0%BD%D0%B8%D1%81", "Теннис и движение", "Хоккейная смена"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, h, tc.target)
			if w.Code != 200 {
				t.Fatalf("status=%d %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if i := strings.Index(body, "<body>"); i >= 0 {
				body = body[i:]
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("missing %q", tc.want)
			}
			if strings.Contains(body, tc.absent) {
				t.Fatalf("unfiltered SSR material visible: %q", tc.absent)
			}
			if strings.Contains(body, `class="hero-grid"`) {
				t.Fatal("filtered page shows hero")
			}
		})
	}
	if w := get(t, h, "/news/hero"); w.Code != 200 || !strings.Contains(w.Body.String(), b.Hero.Title) {
		t.Fatalf("hero article status=%d", w.Code)
	}
	if w := get(t, h, "/news/missing"); w.Code != 404 {
		t.Fatalf("missing article status=%d", w.Code)
	}
}

func TestBookmakerCatalogAndAssetsAreServed(t *testing.T) {
	dir := t.TempDir()
	putBundle(t, dir, "bundle.json", sampleBundle())
	site := makeSite(t, dir)
	defer site.Close()
	h := site.Handler()
	for _, asset := range []string{"/bookmakers-widget.js", "/bookmakers-widget.css"} {
		w := get(t, h, asset)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "<!doctype html>") {
			t.Fatalf("widget asset %s returned status %d or HTML", asset, w.Code)
		}
	}
	w := get(t, h, "/bookmakers.json")
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("catalog status=%d content-type=%s", w.Code, w.Header().Get("Content-Type"))
	}
	var catalog struct {
		Groups map[string]struct {
			Total int               `json:"total"`
			Items []json.RawMessage `json:"items"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"ru", "best"} {
		items := catalog.Groups[group]
		if items.Total == 0 || items.Total != len(items.Items) {
			t.Fatalf("incomplete catalog group %s", group)
		}
	}
	// Only the explicitly public catalog is permitted, not arbitrary JSON files.
	if response := get(t, h, "/assets/private.json"); response.Code != http.StatusNotFound {
		t.Fatalf("arbitrary JSON route exposed: %d", response.Code)
	}
}
func TestActualFixtureHeroRenders(t *testing.T) {
	dir := t.TempDir()
	p, e := os.ReadFile(filepath.Join(backendDir(t), "../data/bundle.fixture.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "bundle.fixture.json"), p, 0600); e != nil {
		t.Fatal(e)
	}
	var b model.Bundle
	if e = json.Unmarshal(p, &b); e != nil {
		t.Fatal(e)
	}
	if b.Hero == nil {
		t.Fatal("fixture hero missing")
	}
	w := get(t, makeSite(t, dir).Handler(), "/news/"+b.Hero.ID)
	if w.Code != 200 || !strings.Contains(w.Body.String(), b.Hero.Title) {
		t.Fatalf("fixture hero status=%d", w.Code)
	}
}
func TestRSSContentAndHTMLEscaping(t *testing.T) {
	dir := t.TempDir()
	b := sampleBundle()
	b.Feed[0].Title = `<script>alert("title")</script>`
	b.Feed[0].Summary = `</script><img src=x onerror="alert(1)">`
	b.Feed[0].URL = "https://example.com/original"
	b.Hero = &b.Feed[0]
	putBundle(t, dir, "bundle.json", b)
	h := makeSite(t, dir).Handler()
	w := get(t, h, "/news/hero")
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `<script>alert`) || strings.Contains(body, `<img src=x onerror=`) || strings.Contains(body, `</script><img`) {
		t.Fatal("RSS HTML rendered as markup or broke JSON script")
	}
	if !strings.Contains(body, `&lt;script&gt;`) || !strings.Contains(body, `&lt;img src=x onerror=`) {
		t.Fatal("escaped RSS text missing")
	}
	r := get(t, h, "/feed.xml")
	if r.Code != 200 || !strings.Contains(r.Header().Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("RSS response %d", r.Code)
	}
	var doc struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if e := xml.Unmarshal(r.Body.Bytes(), &doc); e != nil {
		t.Fatalf("RSS invalid: %v", e)
	}
	if len(doc.Channel.Items) != len(b.Feed) {
		t.Fatalf("RSS items=%d", len(doc.Channel.Items))
	}
	a := doc.Channel.Items[0]
	if a.Title != b.Feed[0].Title || a.Link != b.Feed[0].URL || a.Description != b.Feed[0].Summary {
		t.Fatal("RSS content differs")
	}
}
func TestUnreadableBundleKeepsLastGoodBeforeFixture(t *testing.T) {
	dir := t.TempDir()
	live := sampleBundle()
	live.Feed[0].Title = "Последний рабочий выпуск"
	fixture := sampleBundle()
	fixture.Feed[0].Title = "Демонстрационный выпуск"
	putBundle(t, dir, "bundle.json", live)
	putBundle(t, dir, "bundle.fixture.json", fixture)
	site := makeSite(t, dir)
	h := site.Handler()
	first := get(t, h, "/api/v1/bundle")
	if !strings.Contains(first.Body.String(), live.Feed[0].Title) {
		t.Fatal("live bundle not loaded")
	}
	if e := os.WriteFile(filepath.Join(dir, "bundle.json"), []byte("{partial"), 0600); e != nil {
		t.Fatal(e)
	}
	second := get(t, h, "/api/v1/bundle")
	got, _ := site.bundle()
	if got.Feed[0].Title != live.Feed[0].Title || !strings.Contains(second.Body.String(), live.Feed[0].Title) || strings.Contains(second.Body.String(), fixture.Feed[0].Title) {
		t.Fatal("unreadable live bundle replaced cached good content with fixture")
	}
}
func TestStaticTraversalDoesNotExposeOutsideFile(t *testing.T) {
	dir := t.TempDir()
	putBundle(t, dir, "bundle.json", sampleBundle())
	s := makeSite(t, dir)
	frontend := filepath.Join(dir, "frontend")
	if e := os.MkdirAll(filepath.Join(frontend, "assets"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "secret.svg"), []byte("PRIVATE_FILE_CONTENT"), 0600); e != nil {
		t.Fatal(e)
	}
	s.cfg.FrontendDir = frontend
	h := s.Handler()
	for _, target := range []string{"/assets/../secret.svg", "/assets/%2e%2e%2fsecret.svg", "/assets/..%5csecret.svg"} {
		w := get(t, h, target)
		if w.Code == 200 || strings.Contains(w.Body.String(), "PRIVATE_FILE_CONTENT") {
			t.Fatalf("traversal exposed file: %s status=%d", target, w.Code)
		}
	}
}
func TestAPIPaginationClampsLimitsAndPage(t *testing.T) {
	dir := t.TempDir()
	b := sampleBundle()
	b.Feed = nil
	for i := 0; i < 60; i++ {
		b.Feed = append(b.Feed, model.Article{ID: fmt.Sprintf("item-%02d", i), Title: fmt.Sprintf("Article %d", i), Tag: "football"})
	}
	putBundle(t, dir, "bundle.json", b)
	h := makeSite(t, dir).Handler()
	for _, tc := range []struct {
		target      string
		page, count int
		next        bool
		first       string
	}{
		{"/api/v1/news?limit=999&page=-3", 1, 12, true, "item-00"}, {"/api/v1/news?limit=2&page=2", 2, 2, true, "item-02"}, {"/api/v1/news?limit=48&page=2", 2, 12, false, "item-48"}, {"/api/v1/news?limit=48&page=999999", 10000, 0, false, ""},
	} {
		w := get(t, h, tc.target)
		var got struct {
			Items   []model.Article `json:"items"`
			Total   int             `json:"total"`
			Page    int             `json:"page"`
			HasNext bool            `json:"has_next"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &got); e != nil {
			t.Fatal(e)
		}
		if w.Code != 200 || got.Page != tc.page || len(got.Items) != tc.count || got.HasNext != tc.next || got.Total != 60 {
			t.Fatalf("%s: status=%d page=%d count=%d next=%v total=%d", tc.target, w.Code, got.Page, len(got.Items), got.HasNext, got.Total)
		}
		if tc.first != "" && got.Items[0].ID != tc.first {
			t.Fatalf("%s first=%s", tc.target, got.Items[0].ID)
		}
	}
}

func TestDatabaseAdsAndArchivedArticleArePubliclyRendered(t *testing.T) {
	dir := t.TempDir()
	b := sampleBundle()
	putBundle(t, dir, "bundle.json", b)
	site := makeSite(t, dir)
	defer site.Close()
	if err := site.store.UpsertItems(context.Background(), []model.Article{{ID: "archived", Title: "Архивный матч", Summary: "Сохранённый материал", Tag: "football", URL: "https://example.com/archive", PublishedAt: "2020-01-01T00:00:00Z"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := site.store.DB.Exec(`INSERT INTO ad_creatives(id,placement,title,advertiser,erid,click_url,file,width,height,is_active,starts_at,ends_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "ad-1", "top", "Большая игра начинается здесь", "Acme Sports", "123456", "https://example.com/ad", "/uploads/ads/banner.png", 728, 90, 1, "2020-01-01T00:00:00Z", "2099-01-01T00:00:00Z", "2024-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	h := site.Handler()
	w := get(t, h, "/")
	body := w.Body.String()
	for _, want := range []string{"Acme Sports", "erid: 123456", "/uploads/ads/banner.png", "rel=\"sponsored noopener\""} {
		if !strings.Contains(body, want) {
			t.Fatalf("active ad missing %q", want)
		}
	}
	archived := get(t, h, "/news/archived")
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), "Архивный матч") {
		t.Fatalf("archived article status=%d", archived.Code)
	}
	if _, err := site.store.DB.Exec(`UPDATE ad_creatives SET ends_at=? WHERE id=?`, "2000-01-01T00:00:00Z", "ad-1"); err != nil {
		t.Fatal(err)
	}
	without := get(t, h, "/")
	for _, gone := range []string{"Acme Sports", "erid: 123456", "/uploads/ads/banner.png"} {
		if strings.Contains(without.Body.String(), gone) {
			t.Fatalf("expired ad still rendered: %q", gone)
		}
	}
}
