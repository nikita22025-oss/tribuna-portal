package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tribuna-portal/internal/admin"
	"tribuna-portal/internal/data"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "create-user" {
		createUser(os.Args[2:])
		return
	}
	port := env("ADMIN_PORT", "8081")
	dataDir := env("DATA_DIR", "../data")
	dbPath := env("ADMIN_DB_PATH", filepath.Join(dataDir, "ingester.db"))
	upload := env("ADMIN_UPLOADS_DIR", "../frontend/uploads/ads")
	store, err := data.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	s, err := admin.New(admin.Config{DB: store.DB, UploadDir: upload, SecureCookies: env("ADMIN_SECURE_COOKIES", "true") == "true", Refresh: func(ctx context.Context) error { return store.WriteBundle(ctx, dataDir) }})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("admin listening on 127.0.0.1:%s", port)
	h := &http.Server{Addr: "127.0.0.1:" + port, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = h.Shutdown(shutdown)
	}()
	if err := h.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
func createUser(args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	email := fs.String("email", "", "admin email")
	_ = fs.Parse(args)
	if strings.TrimSpace(*email) == "" {
		log.Fatal("--email is required")
	}
	password := os.Getenv("TRIBUNA_ADMIN_PASSWORD")
	if password == "" {
		log.Fatal("TRIBUNA_ADMIN_PASSWORD is required")
	}
	dbPath := env("ADMIN_DB_PATH", filepath.Join(env("DATA_DIR", "../data"), "ingester.db"))
	store, err := data.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if _, err = store.DB.Exec("CREATE TABLE IF NOT EXISTS admin_users (id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'manager',is_active INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL)"); err != nil {
		log.Fatal(err)
	}
	if err := admin.CreateUser(store.DB, *email, password); err != nil {
		log.Fatal(err)
	}
	fmt.Println("admin user created")
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
