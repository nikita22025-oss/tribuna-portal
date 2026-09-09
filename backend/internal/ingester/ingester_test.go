package ingester

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"tribuna-portal/internal/data"
	"tribuna-portal/internal/model"
)

func TestRunOnceAndFailurePreservesData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>X</title><item><title>One</title><link>https://x/1</link><description><![CDATA[<b>Summary</b>]]></description></item></channel></rss>`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	st, err := data.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ig := &Ingester{Store: st, Config: Config{DataDir: dir, Client: srv.Client()}, Sources: []Source{{ID: "x", Name: "X", URL: srv.URL, Enabled: true}}}
	if err := ig.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, _ := st.ListItems(context.Background(), 10)
	if len(items) != 1 || items[0].Summary != "Summary" {
		t.Fatalf("items=%+v", items)
	}
	ig.Sources = []Source{{ID: "bad", Name: "bad", URL: "http://127.0.0.1:1", Enabled: true}}
	if err := ig.RunOnce(context.Background()); err == nil {
		t.Fatal("expected all-source failure")
	}
	items, _ = st.ListItems(context.Background(), 10)
	if len(items) != 1 {
		t.Fatalf("data lost after failure: %d", len(items))
	}
}

func TestImageDownloadResizesAndRejectsInvalid(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1600, 800)), nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.Write([]byte("not image"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(buf.Bytes())
	}))
	defer srv.Close()
	dir := t.TempDir()
	ig := &Ingester{Config: Config{Client: srv.Client(), ImagesDir: dir}}
	p, err := ig.downloadImage(context.Background(), srv.URL+"/image")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, filepath.Base(p)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 1280 || cfg.Height != 640 {
		t.Fatalf("unexpected resize %dx%d", cfg.Width, cfg.Height)
	}
	if _, err = ig.downloadImage(context.Background(), srv.URL+"/bad"); err == nil {
		t.Fatal("invalid image accepted")
	}
}

func TestCleanupKeepsReferencedMedia(t *testing.T) {
	dir := t.TempDir()
	st, err := data.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	keep := strings.Repeat("a", 64) + ".jpg"
	remove := strings.Repeat("b", 64) + ".jpg"
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{keep, remove} {
		p := filepath.Join(dir, name)
		if err = os.WriteFile(p, []byte("image"), 0644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, old, old)
	}
	if err = st.UpsertItems(context.Background(), []model.Article{{ID: "keep", Title: "Keep", URL: "https://x/keep", ImagePath: "/uploads/news/" + keep}}); err != nil {
		t.Fatal(err)
	}
	ig := &Ingester{Store: st, Config: Config{ImagesDir: dir}}
	if err = ig.CleanupImages(context.Background(), 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, keep)); err != nil {
		t.Fatal("referenced image removed")
	}
	if _, err = os.Stat(filepath.Join(dir, remove)); !os.IsNotExist(err) {
		t.Fatal("orphan image retained")
	}
}
