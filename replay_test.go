package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParseReplayStart(t *testing.T) {
	cases := map[string]string{
		"09:30":  "09:30:00",
		"0930":   "09:30:00",
		"9:30am": "09:30:00",
		"0930am": "09:30:00",
		"930am":  "09:30:00",
	}
	for raw, want := range cases {
		got, ok, err := parseReplayStart(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if !ok {
			t.Fatalf("%s: expected start", raw)
		}
		if got.String() != want {
			t.Fatalf("%s: got %s want %s", raw, got.String(), want)
		}
	}
}

func TestReplayStartSkipsEarlierNewYorkMessages(t *testing.T) {
	cfg := testConfig()
	cfg.Calculation.LookbackSeconds = 1
	cfg.Calculation.MinTodayVolume = 0
	store := &ConfigStore{cfg: cfg}
	sm := mustSession(t, cfg)
	engine := NewEngine([]string{"AAPL"}, sm)
	runner := &FeedRunner{store: store, engine: engine, status: &ConnectionStatus{}}

	dir := t.TempDir()
	path := filepath.Join(dir, "replay.jsonl")
	before := nyTime(t, "2026-06-26 09:29:59").UnixMilli()
	at := nyTime(t, "2026-06-26 09:30:00").UnixMilli()
	after := nyTime(t, "2026-06-26 09:30:01").UnixMilli()
	body := `{"ev":"A","sym":"AAPL","e":` + itoa64(before) + `,"c":99,"v":10}` + "\n" +
		`{"ev":"A","sym":"AAPL","e":` + itoa64(at) + `,"c":100,"v":20}` + "\n" +
		`{"ev":"A","sym":"AAPL","e":` + itoa64(after) + `,"c":101,"v":30}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runner.RunReplay(context.Background(), path, "0930am", 0, 0); err != nil {
		t.Fatal(err)
	}
	st := engine.states["AAPL"]
	if st.TodayVolume != 50 {
		t.Fatalf("volume=%d want 50", st.TodayVolume)
	}
	if len(st.Bars) != 2 {
		t.Fatalf("bars=%d want 2", len(st.Bars))
	}
}

func TestClockOnSessionDate(t *testing.T) {
	cfg := testConfig()
	sm := mustSession(t, cfg)
	anchor := nyTime(t, "2026-06-26 00:00:00")
	start, ok, err := parseReplayStart("0930am")
	if err != nil || !ok {
		t.Fatalf("start parse ok=%v err=%v", ok, err)
	}
	end, ok, err := parseReplayStart("04pm")
	if err != nil || !ok {
		t.Fatalf("end parse ok=%v err=%v", ok, err)
	}
	if got := clockOnSessionDate(anchor, sm.loc, start).Format("15:04:05"); got != "09:30:00" {
		t.Fatalf("start=%s", got)
	}
	if got := clockOnSessionDate(anchor, sm.loc, end).Format("15:04:05"); got != "16:00:00" {
		t.Fatalf("end=%s", got)
	}
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}
