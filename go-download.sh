#!/bin/bash
set -e

today="$(TZ=America/New_York date +%F)"
date="$today"
out=""

if [ "$1" = "--date" ]; then
  if [ -z "$2" ]; then
    echo "usage: ./go-download.sh [output.jsonl] [--date YYYY-MM-DD] [extra go args...]"
    exit 2
  fi
  date="$2"
  shift 2
fi

if [ -n "$1" ] && [[ "$1" != -* ]]; then
  out="$1"
  shift
fi

if [ "$1" = "--date" ]; then
  if [ -z "$2" ]; then
    echo "usage: ./go-download.sh [output.jsonl] [--date YYYY-MM-DD] [extra go args...]"
    exit 2
  fi
  date="$2"
  shift 2
fi

if [ -z "$out" ]; then
  out="replay-${date}.jsonl"
fi

echo "Downloading ${date} to ${out}"
echo "Replay with: ./go-0930.sh ${out}"

exec go run . -watchlist 1000-company-filter.csv -download-date "$date" -download-start 0930am -download-end 04pm -download-out "$out" "$@"
