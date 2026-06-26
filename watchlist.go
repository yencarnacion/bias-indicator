package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func LoadWatchlistCSV(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	seen := map[string]struct{}{}
	var out []string
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(row) == 0 {
			continue
		}
		s := strings.ToUpper(strings.TrimSpace(row[0]))
		if s == "" || strings.HasPrefix(s, "#") || isWatchlistHeader(s) {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("watchlist %s is empty", path)
	}
	sort.Strings(out)
	return out, nil
}

func isWatchlistHeader(s string) bool {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SYMBOL", "TICKER", "TICKERS":
		return true
	default:
		return false
	}
}
