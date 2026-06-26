#!/bin/bash
if [ -z "$1" ]; then
  echo "usage: ./go-0930.sh replay.jsonl [extra go args...]"
  exit 2
fi

replay_file="$1"
shift

exec go run . -watchlist 1000-company-filter.csv -replay "$replay_file" -replay-start 0930am -replay-step-delay 1s -replay-start-delay 10s "$@"
