package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	massivews "github.com/massive-com/client-go/v2/websocket"
	"github.com/massive-com/client-go/v2/websocket/models"
)

type ConnectionStatus struct {
	v atomic.Value
}

func (s *ConnectionStatus) Set(v string) { s.v.Store(v) }
func (s *ConnectionStatus) Get() string {
	if v, ok := s.v.Load().(string); ok && v != "" {
		return v
	}
	return "starting"
}

type FeedRunner struct {
	store  *ConfigStore
	engine *Engine
	status *ConnectionStatus
}

func (f *FeedRunner) RunMassive(ctx context.Context, symbols []string) {
	for {
		if ctx.Err() != nil {
			return
		}
		cfg := f.store.Get()
		key := strings.TrimSpace(os.Getenv(cfg.Massive.APIKeyEnv))
		if key == "" {
			f.status.Set("missing " + cfg.Massive.APIKeyEnv + " - dashboard/replay only")
			select {
			case <-time.After(5 * time.Second):
				continue
			case <-ctx.Done():
				return
			}
		}
		if err := f.connectOnce(ctx, cfg, key, symbols); err != nil && ctx.Err() == nil {
			f.status.Set("reconnecting: " + err.Error())
			log.Printf("[massive] %v", err)
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (f *FeedRunner) connectOnce(ctx context.Context, cfg Config, key string, symbols []string) error {
	client, err := massivews.New(massivews.Config{
		APIKey: key,
		Feed:   massiveFeedFromConfig(cfg),
		Market: massivews.Stocks,
		ReconnectCallback: func(err error) {
			if err != nil {
				f.status.Set("reconnecting: " + err.Error())
				return
			}
			f.status.Set("connected")
		},
	})
	if err != nil {
		return err
	}
	defer client.Close()

	topic := massiveTopic(cfg.Massive.DataMode)
	for _, chunk := range symbolChunks(symbols, 150) {
		if err := client.Subscribe(topic, chunk...); err != nil {
			return err
		}
	}
	if err := client.Connect(); err != nil {
		return err
	}
	f.status.Set("connected")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-client.Error():
			if err != nil {
				return err
			}
			return fmt.Errorf("massive websocket closed")
		case out, ok := <-client.Output():
			if !ok {
				return fmt.Errorf("massive websocket output closed")
			}
			f.handleClientOutput(out, cfg)
		}
	}
}

func massiveFeedFromConfig(cfg Config) massivews.Feed {
	raw := strings.TrimSpace(cfg.Massive.WebSocketURL)
	raw = strings.TrimSuffix(raw, "/")
	raw = strings.TrimSuffix(raw, "/stocks")
	if raw == "" {
		return massivews.RealTime
	}
	return massivews.Feed(raw)
}

func massiveTopic(mode string) massivews.Topic {
	if normalizeDataMode(mode) == "trades" {
		return massivews.StocksTrades
	}
	return massivews.StocksSecAggs
}

func symbolChunks(symbols []string, chunk int) [][]string {
	if chunk <= 0 {
		chunk = len(symbols)
	}
	var out [][]string
	for i := 0; i < len(symbols); i += chunk {
		end := i + chunk
		if end > len(symbols) {
			end = len(symbols)
		}
		out = append(out, symbols[i:end])
	}
	return out
}

func (f *FeedRunner) RunReplay(ctx context.Context, path, startRaw string, stepDelay, startDelay time.Duration) error {
	startClock, hasStart, err := parseReplayStart(startRaw)
	if err != nil {
		return err
	}
	if startDelay > 0 {
		f.status.Set("replay waiting " + startDelay.String())
		select {
		case <-time.After(startDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	f.status.Set("replay: " + path)
	if hasStart {
		f.status.Set("replay: " + path + " from " + startClock.String())
	}
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var lastReplaySecond int64
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cfg := f.store.Get()
		data := []byte(line)
		if hasStart {
			var ok bool
			data, ok = f.filterReplayLine(data, startClock)
			if !ok {
				continue
			}
		}
		if ts, ok := replayLineMaxTimestamp(data); ok {
			replaySecond := ts.Unix()
			if lastReplaySecond != 0 && replaySecond > lastReplaySecond && stepDelay > 0 {
				select {
				case <-time.After(stepDelay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			f.engine.SetNowOverride(ts)
			lastReplaySecond = replaySecond
		}
		f.handleMessage(data, cfg)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	f.status.Set("replay complete")
	return nil
}

func replayLineMaxTimestamp(b []byte) (time.Time, bool) {
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		var one map[string]any
		if err := json.Unmarshal(b, &one); err != nil {
			return time.Time{}, false
		}
		arr = []map[string]any{one}
	}
	var maxTS time.Time
	for _, m := range arr {
		bar, ok := replayBarFromMap(m)
		if !ok {
			continue
		}
		if maxTS.IsZero() || bar.TS.After(maxTS) {
			maxTS = bar.TS
		}
	}
	return maxTS, !maxTS.IsZero()
}

func parseReplayStart(raw string) (clockTime, bool, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return clockTime{}, false, nil
	}
	for _, suffix := range []string{"am", "pm"} {
		if strings.HasSuffix(s, suffix) {
			digits := strings.TrimSpace(strings.TrimSuffix(s, suffix))
			if len(digits) == 3 || len(digits) == 4 {
				if _, err := strconv.Atoi(digits); err == nil {
					if len(digits) == 3 {
						digits = "0" + digits
					}
					s = digits[:2] + ":" + digits[2:] + suffix
				}
			}
		}
	}
	if len(s) == 3 || len(s) == 4 {
		if _, err := strconv.Atoi(s); err == nil {
			if len(s) == 3 {
				s = "0" + s
			}
			s = s[:2] + ":" + s[2:]
		}
	}
	layouts := []string{"15:04:05", "15:04", "3:04pm", "03:04pm", "3pm", "03pm", "3:04 pm", "03:04 pm", "3 pm", "03 pm"}
	for _, layout := range layouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return clockTime{h: t.Hour(), m: t.Minute(), s: t.Second()}, true, nil
		}
	}
	return clockTime{}, false, fmt.Errorf("invalid replay start time %q (use 09:30, 0930, or 9:30am)", raw)
}

func (f *FeedRunner) filterReplayLine(b []byte, start clockTime) ([]byte, bool) {
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		var one map[string]any
		if err := json.Unmarshal(b, &one); err != nil {
			return b, true
		}
		arr = []map[string]any{one}
	}
	kept := make([]map[string]any, 0, len(arr))
	for _, m := range arr {
		bar, ok := replayBarFromMap(m)
		if !ok {
			kept = append(kept, m)
			continue
		}
		t := bar.TS.In(f.engine.session.loc)
		c := clockTime{h: t.Hour(), m: t.Minute(), s: t.Second()}
		if !clockBefore(c, start) {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return nil, false
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return b, true
	}
	return out, true
}

func replayBarFromMap(m map[string]any) (Bar, bool) {
	ev := strings.ToUpper(strVal(m, "ev"))
	if ev == "T" {
		return tradeBarFromMap(m)
	}
	return aggregateBarFromMap(m)
}

func clockBefore(a, b clockTime) bool {
	if a.h != b.h {
		return a.h < b.h
	}
	if a.m != b.m {
		return a.m < b.m
	}
	return a.s < b.s
}

func (c clockTime) String() string {
	return fmt.Sprintf("%02d:%02d:%02d", c.h, c.m, c.s)
}

func (f *FeedRunner) handleMessage(b []byte, cfg Config) {
	var arr []map[string]any
	if err := json.Unmarshal(b, &arr); err != nil {
		var one map[string]any
		if err := json.Unmarshal(b, &one); err != nil {
			return
		}
		arr = []map[string]any{one}
	}
	for _, m := range arr {
		ev := strings.ToUpper(strVal(m, "ev"))
		status := strings.ToLower(strVal(m, "status"))
		if ev == "STATUS" || status != "" {
			if msg := strVal(m, "message"); msg != "" {
				f.status.Set(status + ": " + msg)
			}
			continue
		}
		switch ev {
		case "A", "AM":
			if bar, ok := aggregateBarFromMap(m); ok {
				f.engine.ApplyBar(bar, cfg)
			}
		case "T":
			if bar, ok := tradeBarFromMap(m); ok {
				f.engine.ApplyBar(bar, cfg)
			}
		default:
			if bar, ok := aggregateBarFromMap(m); ok {
				f.engine.ApplyBar(bar, cfg)
			}
		}
	}
}

func (f *FeedRunner) handleClientOutput(out any, cfg Config) {
	switch msg := out.(type) {
	case models.EquityAgg:
		bar := Bar{
			Symbol: strings.ToUpper(strings.TrimSpace(msg.Symbol)),
			TS:     unixMaybeMillis(msg.EndTimestamp),
			Close:  msg.Close,
			Volume: int64(math.Round(msg.Volume)),
		}
		f.engine.ApplyBar(bar, cfg)
	case models.EquityTrade:
		bar := Bar{
			Symbol: strings.ToUpper(strings.TrimSpace(msg.Symbol)),
			TS:     unixMaybeMillis(msg.Timestamp),
			Close:  msg.Price,
			Volume: msg.Size,
		}
		f.engine.ApplyBar(bar, cfg)
	case models.ControlMessage:
		if msg.Status != "" || msg.Message != "" {
			text := strings.TrimSpace(strings.TrimSpace(msg.Status) + ": " + strings.TrimSpace(msg.Message))
			f.status.Set(text)
		}
	}
}

func aggregateBarFromMap(m map[string]any) (Bar, bool) {
	sym := strings.ToUpper(strings.TrimSpace(firstStr(m, "sym", "symbol", "ticker")))
	closePx := firstFloat(m, "c", "close", "price")
	vol := int64(math.Round(firstFloat(m, "v", "volume", "size")))
	tsms := int64(firstFloat(m, "e", "s", "t", "timestamp"))
	if tsms <= 0 || sym == "" || closePx <= 0 {
		return Bar{}, false
	}
	return Bar{Symbol: sym, TS: unixMaybeMillis(tsms), Close: closePx, Volume: vol}, true
}

func tradeBarFromMap(m map[string]any) (Bar, bool) {
	sym := strings.ToUpper(strings.TrimSpace(firstStr(m, "sym", "symbol", "ticker")))
	price := firstFloat(m, "p", "price")
	vol := int64(math.Round(firstFloat(m, "s", "size", "volume")))
	tsms := int64(firstFloat(m, "t", "timestamp"))
	if tsms <= 0 || sym == "" || price <= 0 {
		return Bar{}, false
	}
	return Bar{Symbol: sym, TS: unixMaybeMillis(tsms), Close: price, Volume: vol}, true
}

func unixMaybeMillis(v int64) time.Time {
	if v > 1_000_000_000_000_000 {
		return time.Unix(0, v)
	}
	if v > 1_000_000_000_000 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := strVal(m, k); v != "" {
			return v
		}
	}
	return ""
}

func strVal(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func firstFloat(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch x := v.(type) {
			case float64:
				return x
			case int:
				return float64(x)
			case int64:
				return float64(x)
			case json.Number:
				f, _ := x.Float64()
				return f
			case string:
				f, _ := strconv.ParseFloat(x, 64)
				return f
			}
		}
	}
	return 0
}
