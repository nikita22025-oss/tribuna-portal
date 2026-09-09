package data

import (
	"context"
	"path/filepath"
	"testing"
	"tribuna-portal/internal/model"
)

func TestUpsertDeduplicates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := model.Article{ID: "n-1", URL: "https://x/1", Title: "A", PublishedAt: "2026-01-01T00:00:00Z"}
	if err = s.UpsertItems(context.Background(), []model.Article{a, a}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListItems(context.Background(), 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d err %v", len(got), err)
	}
}
func TestWriteBundleAtomic(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "x.db"))
	defer s.Close()
	dir := t.TempDir()
	if err := s.WriteBundle(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
}
