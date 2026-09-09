package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"tribuna-portal/internal/data"
	"tribuna-portal/internal/ingester"
	"tribuna-portal/internal/netguard"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func main() {
	once := flag.Bool("once", false, "run one ingest cycle")
	flag.Parse()
	dataDir := env("DATA_DIR", "../data")
	sourcePath := env("INGESTER_SOURCES", dataDir+"/sources.json")
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		sourcePath = "../data/sources.json"
	}
	db, err := data.Open(dataDir + "/ingester.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	interval := time.Minute
	fetchImages := os.Getenv("FETCH_IMAGES") == "1" || os.Getenv("FETCH_IMAGES") == "true"
	imagesDir := env("INGESTER_IMAGES_DIR", filepath.Join(dataDir, "../frontend/uploads/news"))
	if v, err := strconv.Atoi(os.Getenv("INGESTER_INTERVAL_SECONDS")); err == nil && v > 0 {
		interval = time.Duration(v) * time.Second
	}
	client := netguard.Client(20 * time.Second)
	if os.Getenv("TRIBUNA_SYNTHETIC_DNS") == "true" {
		sources, e := ingester.LoadSources(sourcePath)
		if e != nil {
			log.Fatal(e)
		}
		hosts := []string{}
		for _, source := range sources {
			if u, e := url.Parse(source.URL); e == nil {
				hosts = append(hosts, u.Hostname())
			}
		}
		client = netguard.ClientWithSyntheticDNS(20*time.Second, hosts)
	}
	ig, err := ingester.New(db, ingester.Config{SourcesPath: sourcePath, DataDir: dataDir, Interval: interval, FetchImages: fetchImages, ImagesDir: imagesDir, Client: client, ValidatePublic: true})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if *once {
		if err := ig.RunOnce(ctx); err != nil {
			log.Fatal(err)
		}
		return
	}
	healthPort := 8082
	if v, err := strconv.Atoi(os.Getenv("INGESTER_PORT")); err == nil && v > 0 {
		healthPort = v
	}
	go func() {
		if err := ingester.ServeHealth(ctx, db, healthPort); err != nil && err != http.ErrServerClosed {
			log.Printf("ingester health: %v", err)
		}
	}()
	log.Printf("tribuna ingester started interval=%s sources=%d", interval, len(ig.Sources))
	if err := ig.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}
