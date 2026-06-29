package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type ServerDeps struct {
	Store         *ConfigStore
	Engine        *Engine
	Hub           *EventHub
	Status        *ConnectionStatus
	ReloadSymbols func() ([]string, error)
}

func NewHTTPHandler(deps ServerDeps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/events", deps.Hub)
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, deps.Engine.Snapshot(deps.Store.Get(), time.Now(), deps.Status.Get()))
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, deps.Store.Get())
		case http.MethodPut:
			var incoming Config
			if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			if err := validateSoundFile(incoming.Alerts.Up.SoundFile); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := validateSoundFile(incoming.Alerts.Down.SoundFile); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := validateSoundFile(incoming.Alerts.BothSoundFile); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			next, err := deps.Store.Update(func(c *Config) error {
				*c = incoming
				return nil
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, next)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/watchlist/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		symbols, err := deps.ReloadSymbols()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		deps.Engine.SetSymbols(symbols)
		writeJSON(w, map[string]any{"ok": true, "symbols": symbols})
	})
	mux.HandleFunc("/sound", func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.URL.Query().Get("file"))
		if raw == "" || filepath.IsAbs(raw) || strings.Contains(raw, "..") {
			http.Error(w, "bad file", http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, filepath.Clean(raw))
	})
	static := http.FileServer(http.Dir("web"))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		static.ServeHTTP(w, r)
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func startHTTP(ctx context.Context, cfg Config, h http.Handler) error {
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errc
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
