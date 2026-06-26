package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	configPath := flag.String("config", "config.yaml", "config file")
	portOverride := flag.Int("port", 0, "override server port")
	watchlistOverride := flag.String("watchlist", "", "override watchlist CSV file")
	replayPath := flag.String("replay", "", "replay JSONL feed instead of live Massive websocket")
	replayStart := flag.String("replay-start", "", "skip replay messages before this New York time, e.g. 09:30 or 0930am")
	replayStepDelay := flag.Duration("replay-step-delay", 5*time.Millisecond, "wall-clock delay for each replay timestamp step")
	replayStartDelay := flag.Duration("replay-start-delay", 0, "wall-clock delay before replay begins, useful for opening the dashboard and arming alerts")
	downloadDate := flag.String("download-date", "", "download per-second aggregate replay data for YYYY-MM-DD and exit")
	downloadOut := flag.String("download-out", "", "output JSONL path for -download-date")
	downloadParallel := flag.Int("download-parallel", 4, "parallel symbol downloads for -download-date")
	downloadStart := flag.String("download-start", "", "download start New York time, e.g. 09:30")
	downloadEnd := flag.String("download-end", "", "download end New York time, e.g. 16:00")
	flag.Parse()

	_ = godotenv.Load()

	store, err := NewConfigStore(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *portOverride > 0 {
		_, err := store.RuntimeOverride(func(c *Config) error {
			c.Server.Port = *portOverride
			return nil
		})
		if err != nil {
			log.Fatalf("port override: %v", err)
		}
	}
	if *watchlistOverride == "" && flag.NArg() > 0 {
		*watchlistOverride = flag.Arg(0)
	}
	if *watchlistOverride != "" {
		_, err := store.RuntimeOverride(func(c *Config) error {
			c.Watchlist.File = *watchlistOverride
			return nil
		})
		if err != nil {
			log.Fatalf("watchlist override: %v", err)
		}
	}
	cfg := store.Get()
	sm, err := NewSessionManager(cfg)
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	symbols, err := LoadWatchlistCSV(cfg.Watchlist.File)
	if err != nil {
		log.Fatalf("watchlist: %v", err)
	}

	if *downloadDate != "" {
		log.SetFlags(0)
		out := *downloadOut
		if out == "" {
			out = "replay-" + *downloadDate + ".jsonl"
		}
		if err := DownloadReplayJSONL(context.Background(), cfg, symbols, *downloadDate, out, *downloadParallel, *downloadStart, *downloadEnd); err != nil {
			log.Fatalf("download: %v", err)
		}
		log.Printf("downloaded replay JSONL: %s", out)
		return
	}

	engine := NewEngine(symbols, sm)
	hub := NewEventHub()
	status := &ConnectionStatus{}
	status.Set("starting")
	alerts := &AlertManager{}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	feed := &FeedRunner{store: store, engine: engine, status: status}
	if *replayPath != "" {
		go func() {
			if err := feed.RunReplay(ctx, *replayPath, *replayStart, *replayStepDelay, *replayStartDelay); err != nil && ctx.Err() == nil {
				log.Printf("replay: %v", err)
			}
		}()
	} else {
		go feed.RunMassive(ctx, symbols)
	}

	go func() {
		ticker := time.NewTicker(time.Duration(cfg.Calculation.UpdateIntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cfg := store.Get()
				now := engine.Now(time.Now())
				snap := engine.Snapshot(cfg, now, status.Get())
				hub.Broadcast(map[string]any{"type": "snapshot", "snapshot": snap})
				for _, ev := range alerts.Check(snap, cfg, now) {
					hub.Broadcast(ev)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	deps := ServerDeps{
		Store:  store,
		Engine: engine,
		Hub:    hub,
		Status: status,
		ReloadSymbols: func() ([]string, error) {
			return LoadWatchlistCSV(store.Get().Watchlist.File)
		},
	}
	log.Printf("UI: http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	if err := startHTTP(ctx, cfg, NewHTTPHandler(deps)); err != nil {
		log.Fatalf("http: %v", err)
	}
}
