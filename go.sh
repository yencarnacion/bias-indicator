#!/bin/bash
# Optional override: ./go.sh -port 8099
exec go run . -watchlist 1000-company-filter.csv "$@"
