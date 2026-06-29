package main

import (
	"math"
	"sort"
	"sync"
	"time"
)

type Bar struct {
	Symbol string
	TS     time.Time
	Close  float64
	Volume int64
}

type SymbolBias string

const (
	BiasNeutral SymbolBias = "neutral"
	BiasUp      SymbolBias = "up"
	BiasDown    SymbolBias = "down"
)

type symbolState struct {
	Symbol      string
	Bars        []Bar
	TodayVolume int64
	SessionDate string
	LastUpdate  time.Time
	ChangePct   float64
	Bias        SymbolBias
	Warming     bool
	Stale       bool
	Qualified   bool
}

type Engine struct {
	mu          sync.RWMutex
	states      map[string]*symbolState
	allowed     map[string]struct{}
	session     *SessionManager
	nowOverride time.Time
}

type Snapshot struct {
	UpCount           int          `json:"up_count"`
	DownCount         int          `json:"down_count"`
	Delta             int          `json:"delta"`
	BiasScore         float64      `json:"bias_score"`
	Tracked           int          `json:"tracked"`
	Warming           int          `json:"warming"`
	Stale             int          `json:"stale"`
	DataLate          bool         `json:"data_late"`
	DataLagSeconds    int          `json:"data_lag_seconds"`
	MaxFeedLagSeconds int          `json:"max_feed_lag_seconds"`
	LastUpdate        string       `json:"last_update"`
	ConnectionStatus  string       `json:"connection_status"`
	DataMode          string       `json:"data_mode"`
	ActiveSession     bool         `json:"active_session"`
	SessionWindow     string       `json:"session_window"`
	TopUp             []SymbolRank `json:"top_up"`
	TopDown           []SymbolRank `json:"top_down"`
	Config            Config       `json:"config"`
}

type SymbolRank struct {
	Symbol      string  `json:"symbol"`
	ChangePct   float64 `json:"change_pct"`
	TodayVolume int64   `json:"today_volume"`
	Stale       bool    `json:"stale"`
	Warming     bool    `json:"warming"`
}

func NewEngine(symbols []string, sm *SessionManager) *Engine {
	e := &Engine{states: map[string]*symbolState{}, allowed: map[string]struct{}{}, session: sm}
	e.SetSymbols(symbols)
	return e
}

func (e *Engine) SetSymbols(symbols []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	allowed := map[string]struct{}{}
	for _, s := range symbols {
		allowed[s] = struct{}{}
		if e.states[s] == nil {
			e.states[s] = &symbolState{Symbol: s, Bias: BiasNeutral, Warming: true}
		}
	}
	e.allowed = allowed
	for s := range e.states {
		if _, ok := allowed[s]; !ok {
			delete(e.states, s)
		}
	}
}

func (e *Engine) SetNowOverride(ts time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nowOverride = ts
}

func (e *Engine) ClearNowOverride() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nowOverride = time.Time{}
}

func (e *Engine) Now(fallback time.Time) time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.nowOverride.IsZero() {
		return e.nowOverride
	}
	return fallback
}

func (e *Engine) ApplyBar(bar Bar, cfg Config) bool {
	if bar.Symbol == "" || bar.Close <= 0 {
		return false
	}
	if cfg.Session.IgnoreDataOutsideSession && !e.session.InSession(bar.TS) {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.allowed[bar.Symbol]; !ok {
		return false
	}
	st := e.states[bar.Symbol]
	if st == nil {
		st = &symbolState{Symbol: bar.Symbol, Bias: BiasNeutral}
		e.states[bar.Symbol] = st
	}
	sessionDate := e.session.SessionDate(bar.TS)
	if st.SessionDate != sessionDate {
		st.Bars = nil
		st.TodayVolume = 0
		st.Bias = BiasNeutral
		st.SessionDate = sessionDate
	}
	st.Bars = append(st.Bars, bar)
	st.TodayVolume += maxInt64(0, bar.Volume)
	st.LastUpdate = bar.TS
	e.trimLocked(st, cfg)
	e.recalculateLocked(st, cfg, bar.TS)
	return true
}

func (e *Engine) trimLocked(st *symbolState, cfg Config) {
	keep := time.Duration(cfg.Calculation.LookbackSeconds+cfg.Calculation.MaxStaleSeconds+30) * time.Second
	cutoff := st.LastUpdate.Add(-keep)
	i := 0
	for i < len(st.Bars) && st.Bars[i].TS.Before(cutoff) {
		i++
	}
	if i > 0 {
		st.Bars = append([]Bar(nil), st.Bars[i:]...)
	}
}

func (e *Engine) recalculateLocked(st *symbolState, cfg Config, now time.Time) {
	st.Warming = true
	st.Stale = false
	st.Qualified = false
	if len(st.Bars) == 0 {
		return
	}
	if now.Sub(st.LastUpdate) > time.Duration(cfg.Calculation.MaxStaleSeconds)*time.Second {
		st.Stale = true
		return
	}
	latest := st.Bars[len(st.Bars)-1]
	target := latest.TS.Add(-time.Duration(cfg.Calculation.LookbackSeconds) * time.Second)
	var base *Bar
	for i := range st.Bars {
		if st.Bars[i].TS.After(target) {
			break
		}
		base = &st.Bars[i]
	}
	if base == nil || base.Close <= 0 {
		st.Bias = BiasNeutral
		return
	}
	st.Warming = false
	st.ChangePct = ((latest.Close / base.Close) - 1) * 100
	volumeOK := st.TodayVolume >= cfg.Calculation.MinTodayVolume
	st.Qualified = volumeOK
	if !volumeOK {
		st.Bias = BiasNeutral
		return
	}
	switch st.Bias {
	case BiasUp:
		if st.ChangePct < cfg.UpFilter.ExitMovePct {
			st.Bias = BiasNeutral
		}
	case BiasDown:
		if st.ChangePct > -cfg.DownFilter.ExitMovePct {
			st.Bias = BiasNeutral
		}
	default:
		if st.ChangePct >= cfg.UpFilter.MovePct {
			st.Bias = BiasUp
		} else if st.ChangePct <= -cfg.DownFilter.MovePct {
			st.Bias = BiasDown
		}
	}
}

func (e *Engine) Snapshot(cfg Config, now time.Time, connStatus string) Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.nowOverride.IsZero() {
		now = e.nowOverride
	}
	topN := cfg.UI.TopSymbolCount
	var topUp, topDown []SymbolRank
	snap := Snapshot{
		Tracked:           len(e.allowed),
		ConnectionStatus:  connStatus,
		DataMode:          cfg.Massive.DataMode,
		ActiveSession:     e.session.InSession(now),
		SessionWindow:     e.session.WindowLabel(),
		MaxFeedLagSeconds: cfg.Calculation.MaxFeedLagSeconds,
		Config:            cfg,
	}
	var latestData time.Time
	for _, st := range e.states {
		e.recalculateLocked(st, cfg, now)
		if !st.LastUpdate.IsZero() && (latestData.IsZero() || st.LastUpdate.After(latestData)) {
			latestData = st.LastUpdate
		}
		if st.Warming {
			snap.Warming++
		}
		if st.Stale {
			snap.Stale++
		}
		if st.Warming || st.Stale || !st.Qualified {
			continue
		}
		r := SymbolRank{Symbol: st.Symbol, ChangePct: st.ChangePct, TodayVolume: st.TodayVolume}
		if st.Bias == BiasUp {
			snap.UpCount++
			topUp = append(topUp, r)
		}
		if st.Bias == BiasDown {
			snap.DownCount++
			topDown = append(topDown, r)
		}
	}
	if !latestData.IsZero() {
		lag := now.Sub(latestData)
		if lag < 0 {
			lag = 0
		}
		snap.DataLagSeconds = int(lag.Round(time.Second).Seconds())
		snap.DataLate = snap.ActiveSession && lag > time.Duration(cfg.Calculation.MaxFeedLagSeconds)*time.Second
	}
	sort.Slice(topUp, func(i, j int) bool { return topUp[i].ChangePct > topUp[j].ChangePct })
	sort.Slice(topDown, func(i, j int) bool { return topDown[i].ChangePct < topDown[j].ChangePct })
	if topN > 0 {
		if len(topUp) > topN {
			topUp = topUp[:topN]
		}
		if len(topDown) > topN {
			topDown = topDown[:topN]
		}
	}
	snap.TopUp = topUp
	snap.TopDown = topDown
	if snap.DataLate {
		snap.UpCount = 0
		snap.DownCount = 0
		snap.Warming = 0
		snap.Stale = 0
		snap.TopUp = nil
		snap.TopDown = nil
	}
	snap.Delta = snap.UpCount - snap.DownCount
	den := math.Max(1, float64(snap.UpCount+snap.DownCount))
	snap.BiasScore = 100 * float64(snap.Delta) / den
	snap.LastUpdate = e.session.NowString(now)
	return snap
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
