package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	massive "github.com/massive-com/client-go/v2/rest"
	"github.com/massive-com/client-go/v2/rest/models"
)

type replayAggEvent struct {
	Event  string  `json:"ev"`
	Symbol string  `json:"sym"`
	Start  int64   `json:"s,omitempty"`
	End    int64   `json:"e"`
	Open   float64 `json:"o,omitempty"`
	High   float64 `json:"h,omitempty"`
	Low    float64 `json:"l,omitempty"`
	Close  float64 `json:"c"`
	Volume float64 `json:"v,omitempty"`
	VWAP   float64 `json:"vw,omitempty"`
}

func DownloadReplayJSONL(ctx context.Context, cfg Config, symbols []string, dateRaw, outPath string, parallel int, startRaw, endRaw string) error {
	key := strings.TrimSpace(os.Getenv(cfg.Massive.APIKeyEnv))
	if key == "" {
		return fmt.Errorf("%s is required in .env or environment", cfg.Massive.APIKeyEnv)
	}
	sm, err := NewSessionManager(cfg)
	if err != nil {
		return err
	}
	anchor, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(dateRaw), sm.loc)
	if err != nil {
		return fmt.Errorf("download date must be YYYY-MM-DD: %w", err)
	}
	from, to := sm.Bounds(anchor)
	if strings.TrimSpace(startRaw) != "" {
		startClock, ok, err := parseReplayStart(startRaw)
		if err != nil {
			return err
		}
		if ok {
			from = clockOnSessionDate(anchor, sm.loc, startClock)
		}
	}
	if strings.TrimSpace(endRaw) != "" {
		endClock, ok, err := parseReplayStart(endRaw)
		if err != nil {
			return err
		}
		if ok {
			to = clockOnSessionDate(anchor, sm.loc, endClock)
		}
	}
	if !from.Before(to) {
		return fmt.Errorf("download start must be before end")
	}
	if parallel <= 0 {
		parallel = 1
	}
	windowLabel := fmt.Sprintf("%s %s-%s ET", from.In(sm.loc).Format("2006-01-02"), from.In(sm.loc).Format("15:04:05"), to.In(sm.loc).Format("15:04:05"))
	log.Printf("[download] window %s", windowLabel)

	client := massive.New(key)
	jobs := make(chan string)
	var mu sync.Mutex
	var events []replayAggEvent
	var firstErr error
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for sym := range jobs {
			es, err := downloadSymbolAggs(ctx, client, sym, from, to)
			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			events = append(events, es...)
			mu.Unlock()
			if err != nil {
				log.Printf("[download] %s failed for %s: %v", sym, windowLabel, err)
				continue
			}
			log.Printf("[download] %s %d bars for %s", sym, len(es), windowLabel)
		}
	}

	wg.Add(parallel)
	for i := 0; i < parallel; i++ {
		go worker()
	}
	for _, sym := range symbols {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- sym:
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil && len(events) == 0 {
		return firstErr
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].End == events[j].End {
			return events[i].Symbol < events[j].Symbol
		}
		return events[i].End < events[j].End
	})
	return writeReplayJSONL(outPath, events)
}

func clockOnSessionDate(anchor time.Time, loc *time.Location, c clockTime) time.Time {
	y, m, d := anchor.In(loc).Date()
	return time.Date(y, m, d, c.h, c.m, c.s, 0, loc)
}

func downloadSymbolAggs(ctx context.Context, client *massive.Client, sym string, from, to time.Time) ([]replayAggEvent, error) {
	params := models.ListAggsParams{
		Ticker:     sym,
		Multiplier: 1,
		Timespan:   models.Second,
		From:       models.Millis(from),
		To:         models.Millis(to),
	}.WithAdjusted(true).WithOrder(models.Asc).WithLimit(50000)

	iter := client.ListAggs(ctx, params)
	var out []replayAggEvent
	for iter.Next() {
		agg := iter.Item()
		ts := time.Time(agg.Timestamp)
		if ts.Before(from) || ts.After(to) {
			continue
		}
		out = append(out, replayAggEvent{
			Event:  "A",
			Symbol: sym,
			Start:  ts.UnixMilli(),
			End:    ts.UnixMilli(),
			Open:   agg.Open,
			High:   agg.High,
			Low:    agg.Low,
			Close:  agg.Close,
			Volume: math.Round(agg.Volume),
			VWAP:   agg.VWAP,
		})
	}
	if err := iter.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func writeReplayJSONL(path string, events []replayAggEvent) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return f.Sync()
}
