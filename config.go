package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Massive struct {
		APIKeyEnv    string `yaml:"api_key_env" json:"api_key_env"`
		WebSocketURL string `yaml:"websocket_url" json:"websocket_url"`
		DataMode     string `yaml:"data_mode" json:"data_mode"`
	} `yaml:"massive" json:"massive"`
	Server struct {
		Host string `yaml:"host" json:"host"`
		Port int    `yaml:"port" json:"port"`
	} `yaml:"server" json:"server"`
	Watchlist struct {
		File string `yaml:"file" json:"file"`
	} `yaml:"watchlist" json:"watchlist"`
	Session struct {
		Timezone                 string `yaml:"timezone" json:"timezone"`
		StartTime                string `yaml:"start_time" json:"start_time"`
		EndTime                  string `yaml:"end_time" json:"end_time"`
		ResetAtStartTime         bool   `yaml:"reset_at_start_time" json:"reset_at_start_time"`
		IgnoreDataOutsideSession bool   `yaml:"ignore_data_outside_session" json:"ignore_data_outside_session"`
	} `yaml:"session" json:"session"`
	Calculation struct {
		LookbackSeconds               int   `yaml:"lookback_seconds" json:"lookback_seconds"`
		UpdateIntervalSeconds         int   `yaml:"update_interval_seconds" json:"update_interval_seconds"`
		MaxStaleSeconds               int   `yaml:"max_stale_seconds" json:"max_stale_seconds"`
		MinTodayVolume                int64 `yaml:"min_today_volume" json:"min_today_volume"`
		MaintainInternalSessionVolume bool  `yaml:"maintain_internal_session_volume" json:"maintain_internal_session_volume"`
	} `yaml:"calculation" json:"calculation"`
	UpFilter struct {
		MovePct     float64 `yaml:"move_pct" json:"move_pct"`
		ExitMovePct float64 `yaml:"exit_move_pct" json:"exit_move_pct"`
	} `yaml:"up_filter" json:"up_filter"`
	DownFilter struct {
		MovePct     float64 `yaml:"move_pct" json:"move_pct"`
		ExitMovePct float64 `yaml:"exit_move_pct" json:"exit_move_pct"`
	} `yaml:"down_filter" json:"down_filter"`
	Alerts struct {
		MasterMute            bool            `yaml:"master_mute" json:"master_mute"`
		CooldownSeconds       int             `yaml:"cooldown_seconds" json:"cooldown_seconds"`
		Volume                float64         `yaml:"volume" json:"volume"`
		MaxSimultaneousSounds int             `yaml:"max_simultaneous_sounds" json:"max_simultaneous_sounds"`
		OnSoundLimit          string          `yaml:"on_sound_limit" json:"on_sound_limit"`
		BothSoundFile         string          `yaml:"both_sound_file" json:"both_sound_file"`
		Up                    SideAlertConfig `yaml:"up" json:"up"`
		Down                  SideAlertConfig `yaml:"down" json:"down"`
	} `yaml:"alerts" json:"alerts"`
	UI struct {
		Theme                 string `yaml:"theme" json:"theme"`
		ShowTopSymbols        bool   `yaml:"show_top_symbols" json:"show_top_symbols"`
		TopSymbolCount        int    `yaml:"top_symbol_count" json:"top_symbol_count"`
		ShowSessionDefinition bool   `yaml:"show_session_definition" json:"show_session_definition"`
	} `yaml:"ui" json:"ui"`
}

type SideAlertConfig struct {
	Threshold int    `yaml:"threshold" json:"threshold"`
	SoundFile string `yaml:"sound_file" json:"sound_file"`
	Mute      bool   `yaml:"mute" json:"mute"`
}

type ConfigStore struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func DefaultConfig() Config {
	var c Config
	c.Massive.APIKeyEnv = "MASSIVE_API_KEY"
	c.Massive.WebSocketURL = "wss://socket.massive.com/stocks"
	c.Massive.DataMode = "second_aggregates"
	c.Server.Host = "127.0.0.1"
	c.Server.Port = 8080
	c.Watchlist.File = "watchlist.csv"
	c.Session.Timezone = "America/New_York"
	c.Session.StartTime = "04:00:00"
	c.Session.EndTime = "20:00:00"
	c.Session.ResetAtStartTime = true
	c.Session.IgnoreDataOutsideSession = true
	c.Calculation.LookbackSeconds = 120
	c.Calculation.UpdateIntervalSeconds = 1
	c.Calculation.MaxStaleSeconds = 5
	c.Calculation.MinTodayVolume = 1000000
	c.Calculation.MaintainInternalSessionVolume = true
	c.UpFilter.MovePct = 0.4
	c.UpFilter.ExitMovePct = 0.35
	c.DownFilter.MovePct = 0.4
	c.DownFilter.ExitMovePct = 0.35
	c.Alerts.CooldownSeconds = 30
	c.Alerts.Volume = 0.8
	c.Alerts.MaxSimultaneousSounds = 4
	c.Alerts.OnSoundLimit = "drop_newest"
	c.Alerts.BothSoundFile = "sounds/hey.mp3"
	c.Alerts.Up.SoundFile = "sounds/up.wav"
	c.Alerts.Down.SoundFile = "sounds/down.mp3"
	c.UI.Theme = "dark"
	c.UI.ShowTopSymbols = true
	c.UI.TopSymbolCount = 10
	c.UI.ShowSessionDefinition = true
	return c
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	normalizeConfig(&cfg)
	return cfg, nil
}

func NewConfigStore(path string) (*ConfigStore, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	return &ConfigStore{path: path, cfg: cfg}, nil
}

func (s *ConfigStore) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *ConfigStore) Update(mut func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	if err := mut(&next); err != nil {
		return s.cfg, err
	}
	normalizeConfig(&next)
	if err := writeYAMLAtomic(s.path, next); err != nil {
		return s.cfg, err
	}
	s.cfg = next
	return next, nil
}

func (s *ConfigStore) RuntimeOverride(mut func(*Config) error) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	if err := mut(&next); err != nil {
		return s.cfg, err
	}
	normalizeConfig(&next)
	s.cfg = next
	return next, nil
}

func normalizeConfig(c *Config) {
	def := DefaultConfig()
	if strings.TrimSpace(c.Massive.APIKeyEnv) == "" {
		c.Massive.APIKeyEnv = def.Massive.APIKeyEnv
	}
	if strings.TrimSpace(c.Massive.WebSocketURL) == "" {
		c.Massive.WebSocketURL = def.Massive.WebSocketURL
	}
	c.Massive.DataMode = normalizeDataMode(c.Massive.DataMode)
	if c.Server.Host == "" {
		c.Server.Host = def.Server.Host
	}
	if c.Server.Port == 0 {
		c.Server.Port = def.Server.Port
	}
	if strings.TrimSpace(c.Watchlist.File) == "" {
		c.Watchlist.File = def.Watchlist.File
	}
	if strings.TrimSpace(c.Session.Timezone) == "" {
		c.Session.Timezone = def.Session.Timezone
	}
	if strings.TrimSpace(c.Session.StartTime) == "" {
		c.Session.StartTime = def.Session.StartTime
	}
	if strings.TrimSpace(c.Session.EndTime) == "" {
		c.Session.EndTime = def.Session.EndTime
	}
	if c.Calculation.LookbackSeconds <= 0 {
		c.Calculation.LookbackSeconds = def.Calculation.LookbackSeconds
	}
	if c.Calculation.UpdateIntervalSeconds <= 0 {
		c.Calculation.UpdateIntervalSeconds = def.Calculation.UpdateIntervalSeconds
	}
	if c.Calculation.MaxStaleSeconds <= 0 {
		c.Calculation.MaxStaleSeconds = def.Calculation.MaxStaleSeconds
	}
	if c.Calculation.MinTodayVolume < 0 {
		c.Calculation.MinTodayVolume = def.Calculation.MinTodayVolume
	}
	if c.UpFilter.MovePct <= 0 {
		c.UpFilter.MovePct = def.UpFilter.MovePct
	}
	if c.UpFilter.ExitMovePct <= 0 || c.UpFilter.ExitMovePct > c.UpFilter.MovePct {
		c.UpFilter.ExitMovePct = c.UpFilter.MovePct
	}
	if c.DownFilter.MovePct <= 0 {
		c.DownFilter.MovePct = def.DownFilter.MovePct
	}
	if c.DownFilter.ExitMovePct <= 0 || c.DownFilter.ExitMovePct > c.DownFilter.MovePct {
		c.DownFilter.ExitMovePct = c.DownFilter.MovePct
	}
	if c.Alerts.CooldownSeconds < 0 {
		c.Alerts.CooldownSeconds = def.Alerts.CooldownSeconds
	}
	if c.Alerts.Volume < 0 {
		c.Alerts.Volume = 0
	}
	if c.Alerts.Volume > 1 {
		c.Alerts.Volume = 1
	}
	if c.Alerts.MaxSimultaneousSounds <= 0 {
		c.Alerts.MaxSimultaneousSounds = def.Alerts.MaxSimultaneousSounds
	}
	if c.Alerts.OnSoundLimit != "stop_oldest" {
		c.Alerts.OnSoundLimit = "drop_newest"
	}
	if strings.TrimSpace(c.Alerts.BothSoundFile) == "" {
		c.Alerts.BothSoundFile = def.Alerts.BothSoundFile
	}
	if strings.TrimSpace(c.Alerts.Up.SoundFile) == "" {
		c.Alerts.Up.SoundFile = def.Alerts.Up.SoundFile
	}
	if strings.TrimSpace(c.Alerts.Down.SoundFile) == "" {
		c.Alerts.Down.SoundFile = def.Alerts.Down.SoundFile
	}
	if c.UI.TopSymbolCount <= 0 {
		c.UI.TopSymbolCount = def.UI.TopSymbolCount
	}
}

func normalizeDataMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "trades", "trade", "tick", "ticks":
		return "trades"
	default:
		return "second_aggregates"
	}
}

func writeYAMLAtomic(path string, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

func validateSoundFile(path string) error {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	if ext != ".wav" && ext != ".mp3" {
		return fmt.Errorf("sound file must be .wav or .mp3: %s", path)
	}
	return nil
}
