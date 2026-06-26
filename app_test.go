package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.Calculation.MinTodayVolume = 0
	cfg.Calculation.MaxStaleSeconds = 5
	cfg.Calculation.LookbackSeconds = 120
	return cfg
}

func mustSession(t *testing.T, cfg Config) *SessionManager {
	t.Helper()
	sm, err := NewSessionManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return sm
}

func nyTime(t *testing.T, raw string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", raw, loc)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestLoadWatchlistCSVDedupesAndUppercases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watchlist.csv")
	if err := os.WriteFile(path, []byte("Symbol,Name\n aapl ,Apple\n\nMSFT,Microsoft\naapl,Duplicate\n# comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadWatchlistCSV(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"AAPL", "MSFT"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLoadCompanyFilterCSVFixture(t *testing.T) {
	got, err := LoadWatchlistCSV("1000-company-filter.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 1000 {
		t.Fatalf("loaded %d symbols, expected at least 1000", len(got))
	}
	for _, s := range got {
		if s == "SYMBOL" {
			t.Fatal("header row was loaded as a ticker")
		}
	}
	foundAAPL := false
	for _, s := range got {
		if s == "AAPL" {
			foundAAPL = true
			break
		}
	}
	if !foundAAPL {
		t.Fatal("expected AAPL from company filter CSV")
	}
}

func TestConfigDefaultsAndSoundValidation(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Massive.DataMode != "second_aggregates" || cfg.Session.Timezone != "America/New_York" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if err := validateSoundFile("sounds/up.wav"); err != nil {
		t.Fatal(err)
	}
	if err := validateSoundFile("sounds/down.mp3"); err != nil {
		t.Fatal(err)
	}
	if err := validateSoundFile("sounds/nope.ogg"); err == nil {
		t.Fatal("expected invalid sound extension")
	}
}

func TestSessionUsesNewYorkDSTAndWindow(t *testing.T) {
	cfg := testConfig()
	sm := mustSession(t, cfg)
	winter := nyTime(t, "2026-01-15 04:00:00")
	summer := nyTime(t, "2026-07-15 04:00:00")
	if !sm.InSession(winter) || !sm.InSession(summer) {
		t.Fatal("expected 04:00 ET to be in session in winter and summer")
	}
	if sm.InSession(nyTime(t, "2026-07-15 03:59:59")) {
		t.Fatal("03:59:59 ET should be outside session")
	}
	if sm.InSession(nyTime(t, "2026-07-15 20:00:01")) {
		t.Fatal("20:00:01 ET should be outside session")
	}
}

func TestEngineIgnoresOutsideSessionAndTracksInternalVolume(t *testing.T) {
	cfg := testConfig()
	sm := mustSession(t, cfg)
	e := NewEngine([]string{"AAPL"}, sm)
	if e.ApplyBar(Bar{Symbol: "AAPL", TS: nyTime(t, "2026-06-26 03:59:59"), Close: 100, Volume: 500}, cfg) {
		t.Fatal("outside session bar should be ignored")
	}
	e.ApplyBar(Bar{Symbol: "AAPL", TS: nyTime(t, "2026-06-26 04:00:00"), Close: 100, Volume: 500}, cfg)
	e.ApplyBar(Bar{Symbol: "AAPL", TS: nyTime(t, "2026-06-26 04:02:00"), Close: 101, Volume: 700}, cfg)
	s := e.states["AAPL"]
	if s.TodayVolume != 1200 {
		t.Fatalf("volume=%d want 1200", s.TodayVolume)
	}
}

func TestEngineSessionResetAtFourAM(t *testing.T) {
	cfg := testConfig()
	sm := mustSession(t, cfg)
	e := NewEngine([]string{"AAPL"}, sm)
	e.ApplyBar(Bar{Symbol: "AAPL", TS: nyTime(t, "2026-06-25 19:59:00"), Close: 100, Volume: 1000}, cfg)
	e.ApplyBar(Bar{Symbol: "AAPL", TS: nyTime(t, "2026-06-26 04:00:00"), Close: 101, Volume: 10}, cfg)
	if got := e.states["AAPL"].TodayVolume; got != 10 {
		t.Fatalf("new session volume=%d want 10", got)
	}
}

func TestRollingPercentChangeQualificationAndHysteresis(t *testing.T) {
	cfg := testConfig()
	cfg.UpFilter.MovePct = 0.4
	cfg.UpFilter.ExitMovePct = 0.35
	sm := mustSession(t, cfg)
	e := NewEngine([]string{"AAPL"}, sm)
	t0 := nyTime(t, "2026-06-26 10:00:00")
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0, Close: 100, Volume: 100}, cfg)
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0.Add(120 * time.Second), Close: 100.41, Volume: 100}, cfg)
	snap := e.Snapshot(cfg, t0.Add(120*time.Second), "test")
	if snap.UpCount != 1 {
		t.Fatalf("up_count=%d want 1", snap.UpCount)
	}
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0.Add(121 * time.Second), Close: 100.36, Volume: 100}, cfg)
	snap = e.Snapshot(cfg, t0.Add(121*time.Second), "test")
	if snap.UpCount != 1 {
		t.Fatalf("hysteresis should keep up active, got %d", snap.UpCount)
	}
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0.Add(122 * time.Second), Close: 100.34, Volume: 100}, cfg)
	snap = e.Snapshot(cfg, t0.Add(122*time.Second), "test")
	if snap.UpCount != 0 {
		t.Fatalf("hysteresis should exit below 0.35, got %d", snap.UpCount)
	}
}

func TestStaleAndWarmingExclusions(t *testing.T) {
	cfg := testConfig()
	sm := mustSession(t, cfg)
	e := NewEngine([]string{"AAPL", "MSFT"}, sm)
	t0 := nyTime(t, "2026-06-26 10:00:00")
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0, Close: 100, Volume: 100}, cfg)
	snap := e.Snapshot(cfg, t0.Add(1*time.Second), "test")
	if snap.Warming != 2 {
		t.Fatalf("warming=%d want 2", snap.Warming)
	}
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0.Add(120 * time.Second), Close: 101, Volume: 100}, cfg)
	snap = e.Snapshot(cfg, t0.Add(126*time.Second), "test")
	if snap.Stale != 1 || snap.UpCount != 0 {
		t.Fatalf("stale=%d up=%d want stale 1 up 0", snap.Stale, snap.UpCount)
	}
}

func TestDownQualificationAndBiasScore(t *testing.T) {
	cfg := testConfig()
	sm := mustSession(t, cfg)
	e := NewEngine([]string{"AAPL", "MSFT"}, sm)
	t0 := nyTime(t, "2026-06-26 10:00:00")
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0, Close: 100, Volume: 1}, cfg)
	e.ApplyBar(Bar{Symbol: "MSFT", TS: t0, Close: 100, Volume: 1}, cfg)
	e.ApplyBar(Bar{Symbol: "AAPL", TS: t0.Add(120 * time.Second), Close: 101, Volume: 1}, cfg)
	e.ApplyBar(Bar{Symbol: "MSFT", TS: t0.Add(120 * time.Second), Close: 99, Volume: 1}, cfg)
	snap := e.Snapshot(cfg, t0.Add(120*time.Second), "test")
	if snap.UpCount != 1 || snap.DownCount != 1 || snap.BiasScore != 0 {
		t.Fatalf("snapshot=%+v", snap)
	}
}

func TestAlertThresholdMetEveryUpdateAndSoundLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Alerts.Up.Threshold = 1
	cfg.Alerts.CooldownSeconds = 30
	cfg.Alerts.Up.SoundFile = "sounds/up.wav"
	now := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	am := &AlertManager{}
	events := am.Check(Snapshot{UpCount: 1}, cfg, now)
	if len(events) != 1 {
		t.Fatalf("threshold met should alert, got %d", len(events))
	}
	events = am.Check(Snapshot{UpCount: 2}, cfg, now.Add(time.Second))
	if len(events) != 1 {
		t.Fatalf("above threshold should alert every update, got %d", len(events))
	}
	events = am.Check(Snapshot{UpCount: 2}, cfg, now.Add(2*time.Second))
	if len(events) != 1 {
		t.Fatalf("cooldown should not suppress threshold-met alert, got %d", len(events))
	}
	events = am.Check(Snapshot{UpCount: 0}, cfg, now.Add(3*time.Second))
	if len(events) != 0 {
		t.Fatalf("below threshold should not alert, got %d", len(events))
	}
	active, played := applySoundLimit([]string{"a", "b"}, 2, "drop_newest", "c")
	if played || len(active) != 2 || active[0] != "a" {
		t.Fatalf("drop_newest active=%v played=%v", active, played)
	}
	active, played = applySoundLimit([]string{"a", "b"}, 2, "stop_oldest", "c")
	if !played || len(active) != 2 || active[0] != "b" || active[1] != "c" {
		t.Fatalf("stop_oldest active=%v played=%v", active, played)
	}
}

func TestAlertBothThresholdsUsesHeyOnly(t *testing.T) {
	cfg := testConfig()
	cfg.Alerts.Up.Threshold = 1
	cfg.Alerts.Down.Threshold = 1
	cfg.Alerts.Up.SoundFile = "sounds/up.mp3"
	cfg.Alerts.Down.SoundFile = "sounds/down.mp3"
	cfg.Alerts.BothSoundFile = "sounds/hey.mp3"
	events := (&AlertManager{}).Check(Snapshot{UpCount: 2, DownCount: 3}, cfg, time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC))
	if len(events) != 1 {
		t.Fatalf("events=%d want 1", len(events))
	}
	if events[0].Side != "both" || events[0].SoundFile != "sounds/hey.mp3" {
		t.Fatalf("event=%+v", events[0])
	}
}
