package ingester

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"tribuna-portal/internal/data"
)

// ServeHealth exposes a loopback-only health endpoint for local process supervision.
func ServeHealth(ctx context.Context, store *data.Store, port int) error {
	if port <= 0 {
		port = 8082
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		statuses, err := store.SourceStatuses(r.Context())
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		items, err := store.ListItems(r.Context(), 1)
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		ok := len(statuses) > 0
		for _, s := range statuses {
			if s.Status != "ok" {
				ok = false
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "sources": statuses, "has_items": len(items) > 0})
	})
	srv := &http.Server{Addr: "127.0.0.1:" + strconv.Itoa(port), Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
