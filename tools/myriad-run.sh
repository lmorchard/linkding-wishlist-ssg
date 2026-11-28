#!/bin/sh
# Periodic scrape and build runner for linkding-wishlist-ssg on myriad-docker.
#
# Runs:
# 1. sync-amazon: check for new Amazon wishlist additions and post to Linkding
# 2. scrape: polite price/availability fetch (default 3 careful/Amazon, up to 50 normal)
# 3. build: render static HTML into dist/ with cached local thumbnails
set -e

DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

# Clear any caller-inherited LINKDING_* variables so the local .env is authoritative
unset LINKDING_URL
unset LINKDING_TOKEN

echo "=== Wishlist run started: $(date -u '+%Y-%m-%dT%H:%M:%SZ') ==="

# 1. Incremental Amazon wishlist sync -> Linkding bookmarks
./linkding-wishlist-ssg sync-amazon \
  --cdp-url http://127.0.0.1:9222 \
  --max-pages 3

# 2. Scrape live prices/stock into SQLite store
# Timebox is 15 minutes, leaving clean headroom before the next hourly cycle
./linkding-wishlist-ssg scrape \
  --store ./wishlist.db \
  --cdp-url http://127.0.0.1:9222 \
  --timebox 15m \
  --careful-cap 3 \
  --normal-cap 50

# 3. Build static site with thumbnails
./linkding-wishlist-ssg build \
  --store ./wishlist.db \
  --cache-thumbs \
  --out ./dist

echo "=== Wishlist run finished: $(date -u '+%Y-%m-%dT%H:%M:%SZ') ==="
