package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LOUST-PRO/ical-to-caldav/internal/caldav"
	"github.com/LOUST-PRO/ical-to-caldav/internal/config"
	"github.com/LOUST-PRO/ical-to-caldav/internal/source"
	"github.com/LOUST-PRO/ical-to-caldav/internal/store"
)

var (
	configPath = flag.String("config", "ical-to-caldav.yaml", "Path to config file")
)

func main() {
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	st, err := store.NewBadgerStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fetcher := source.NewFetcher()
	server := caldav.NewServer(st, cfg.Sources)

	// Start background refresh loop
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go refreshLoop(ctx, fetcher, st, cfg.Sources, cfg.RefreshInterval)

	// HTTP server
	mux := server.Handler()
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:     mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
		ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		srv.Shutdown(ctx)
	}()

	fmt.Printf("ical-to-caldav listening on %s\n", cfg.Listen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}

// refreshLoop periodically re-fetches each iCal source.
func refreshLoop(ctx context.Context, fetcher *source.Fetcher, st *store.BadgerStore, sources []config.Source, interval string) {
	ticker := time.NewTicker(parseInterval(interval))
	defer ticker.Stop()

	// Initial fetch
	for _, src := range sources {
		fetchOne(ctx, fetcher, st, src)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, src := range sources {
				fetchOne(ctx, fetcher, st, src)
			}
		}
	}
}

func fetchOne(ctx context.Context, fetcher *source.Fetcher, st *store.BadgerStore, src config.Source) {
	events, err := fetcher.Fetch(ctx, src.URL)
	if err != nil {
		log.Printf("[%s] fetch error: %v", src.ID, err)
		return
	}
	if err := st.SaveSource(src.ID, events); err != nil {
		log.Printf("[%s] save error: %v", src.ID, err)
		return
	}
	if err := st.SetLastFetch(src.ID, time.Now()); err != nil {
		log.Printf("[%s] set last fetch: %v", src.ID, err)
	}
	log.Printf("[%s] synced %d events", src.ID, len(events))
}

func parseInterval(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 15 * time.Minute
	}
	return d
}
