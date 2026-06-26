# Watchlist Bias

Real-time watchlist breadth and short-term momentum bias for U.S. equities.

## Quick Start

1. Put your key in `.env`:

```env
MASSIVE_API_KEY=...
```

2. Edit `watchlist.csv` and `config.yaml`.
3. Run:

```bash
./go.sh
```

4. Open `http://localhost:8080`.

Replay mode:

```bash
./go.sh -replay sample.jsonl
```

Download a replay JSONL from Massive per-second aggregate history. The helper downloads 09:30 AM to 04:00 PM New York time:

```bash
./go-download.sh
./go-0930.sh replay-YYYY-MM-DD.jsonl
```

Override the output file or date:

```bash
./go-download.sh today.jsonl
./go-download.sh replay-2026-06-26.jsonl --date 2026-06-26
```

Start replay from a New York clock time:

```bash
./go.sh -replay sample.jsonl -replay-start 09:30
./go.sh -replay sample.jsonl -replay-start 0930am
```

Use a different watchlist:

```bash
./go.sh -watchlist 1000-company-filter.csv
```

The first positional argument also works:

```bash
./go.sh 1000-company-filter.csv
```

The trading day is defined as `04:00:00` to `20:00:00` in `America/New_York`; data outside that window is ignored for the indicator.
