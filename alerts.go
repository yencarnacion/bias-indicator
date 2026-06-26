package main

import (
	"strings"
	"time"
)

type AlertEvent struct {
	Type      string  `json:"type"`
	Side      string  `json:"side"`
	Count     int     `json:"count"`
	Threshold int     `json:"threshold"`
	SoundFile string  `json:"sound_file"`
	Volume    float64 `json:"volume"`
	At        string  `json:"at"`
}

type AlertManager struct {
}

func (a *AlertManager) Check(s Snapshot, cfg Config, now time.Time) []AlertEvent {
	var out []AlertEvent
	upMet := alertSideMet(s.UpCount, cfg.Alerts.Up)
	downMet := alertSideMet(s.DownCount, cfg.Alerts.Down)
	if cfg.Alerts.MasterMute {
		return out
	}
	if upMet && downMet {
		if ev, ok := a.event("both", maxInt(s.UpCount, s.DownCount), minPositiveThreshold(cfg.Alerts.Up.Threshold, cfg.Alerts.Down.Threshold), cfg.Alerts.BothSoundFile, cfg, now); ok {
			out = append(out, ev)
		}
		return out
	}
	if upMet {
		if ev, ok := a.event("up", s.UpCount, cfg.Alerts.Up.Threshold, cfg.Alerts.Up.SoundFile, cfg, now); ok {
			out = append(out, ev)
		}
	}
	if downMet {
		if ev, ok := a.event("down", s.DownCount, cfg.Alerts.Down.Threshold, cfg.Alerts.Down.SoundFile, cfg, now); ok {
			out = append(out, ev)
		}
	}
	return out
}

func alertSideMet(count int, sideCfg SideAlertConfig) bool {
	return !sideCfg.Mute && sideCfg.Threshold > 0 && count >= sideCfg.Threshold
}

func (a *AlertManager) event(side string, count int, threshold int, soundFile string, cfg Config, now time.Time) (AlertEvent, bool) {
	if strings.TrimSpace(soundFile) == "" {
		return AlertEvent{}, false
	}
	return AlertEvent{
		Type:      "alert",
		Side:      side,
		Count:     count,
		Threshold: threshold,
		SoundFile: soundFile,
		Volume:    cfg.Alerts.Volume,
		At:        now.Format(time.RFC3339),
	}, true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minPositiveThreshold(a, b int) int {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func (a *AlertManager) checkSide(side string, count int, sideCfg SideAlertConfig, cfg Config, now time.Time) (AlertEvent, bool) {
	if cfg.Alerts.MasterMute || !alertSideMet(count, sideCfg) {
		return AlertEvent{}, false
	}
	return a.event(side, count, sideCfg.Threshold, sideCfg.SoundFile, cfg, now)
}

func applySoundLimit(active []string, max int, policy string, newest string) ([]string, bool) {
	if max <= 0 {
		return active, false
	}
	if len(active) < max {
		return append(active, newest), true
	}
	if policy == "stop_oldest" {
		next := append([]string(nil), active[1:]...)
		next = append(next, newest)
		return next, true
	}
	return active, false
}
