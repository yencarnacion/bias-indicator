package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SessionManager struct {
	loc   *time.Location
	start clockTime
	end   clockTime
}

type clockTime struct {
	h int
	m int
	s int
}

func NewSessionManager(cfg Config) (*SessionManager, error) {
	loc, err := time.LoadLocation(cfg.Session.Timezone)
	if err != nil {
		return nil, err
	}
	start, err := parseClock(cfg.Session.StartTime)
	if err != nil {
		return nil, fmt.Errorf("session start: %w", err)
	}
	end, err := parseClock(cfg.Session.EndTime)
	if err != nil {
		return nil, fmt.Errorf("session end: %w", err)
	}
	return &SessionManager{loc: loc, start: start, end: end}, nil
}

func parseClock(raw string) (clockTime, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return clockTime{}, fmt.Errorf("expected HH:MM:SS")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return clockTime{}, err
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return clockTime{}, err
	}
	s, err := strconv.Atoi(parts[2])
	if err != nil {
		return clockTime{}, err
	}
	if h < 0 || h > 23 || m < 0 || m > 59 || s < 0 || s > 59 {
		return clockTime{}, fmt.Errorf("invalid clock")
	}
	return clockTime{h: h, m: m, s: s}, nil
}

func (sm *SessionManager) Bounds(ts time.Time) (time.Time, time.Time) {
	t := ts.In(sm.loc)
	y, m, d := t.Date()
	start := time.Date(y, m, d, sm.start.h, sm.start.m, sm.start.s, 0, sm.loc)
	end := time.Date(y, m, d, sm.end.h, sm.end.m, sm.end.s, 0, sm.loc)
	return start, end
}

func (sm *SessionManager) InSession(ts time.Time) bool {
	t := ts.In(sm.loc)
	start, end := sm.Bounds(t)
	return !t.Before(start) && !t.After(end)
}

func (sm *SessionManager) SessionDate(ts time.Time) string {
	return ts.In(sm.loc).Format("2006-01-02")
}

func (sm *SessionManager) NowString(ts time.Time) string {
	return ts.In(sm.loc).Format("2006-01-02 15:04:05 MST")
}

func (sm *SessionManager) WindowLabel() string {
	return fmt.Sprintf("%02d:%02d:%02d to %02d:%02d:%02d New York time",
		sm.start.h, sm.start.m, sm.start.s, sm.end.h, sm.end.m, sm.end.s)
}
