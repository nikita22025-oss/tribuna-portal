package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"tribuna-portal/internal/handlers"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	site, err := handlers.New(handlers.Config{DataDir: env("DATA_DIR", "../data"), FrontendDir: env("FRONTEND_DIR", "../frontend"), TemplateDir: env("TEMPLATE_DIR", "templates"), AdminURL: env("ADMIN_URL", "http://127.0.0.1:8081"), ContactEmail: os.Getenv("CONTACT_EMAIL")})
	if err != nil {
		log.Fatal(err)
	}
	defer site.Close()
	srv := &http.Server{Addr: env("BIND_ADDR", "127.0.0.1") + ":" + env("PORT", "8080"), Handler: site.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Printf("Трибуна: http://%s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
