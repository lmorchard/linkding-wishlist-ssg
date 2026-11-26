package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/store"
)

func TestScrapeCarefulExcludedWithoutCDPURL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wishlist.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	// Seed one amazon item (careful) and one normal item
	items := []store.Item{
		{
			ItemID:  "amz1",
			URL:     "https://www.amazon.com/dp/B000000001",
			Host:    "amazon.com",
			AddedAt: now,
			Care:    store.Careful,
		},
		{
			ItemID:  "norm1",
			URL:     "https://shop.example/product",
			Host:    "shop.example",
			AddedAt: now,
			Care:    store.Normal,
		},
	}
	if _, err := store.Ingest(db, items, now); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Normal server mock returning JSON-LD
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, `<html><head><script type="application/ld+json">
			{"@type":"Product","offers":{"price":"15.00","priceCurrency":"USD","availability":"https://schema.org/InStock"}}
		</script></head><body>Hello</body></html>`)
	}))
	defer ts.Close()

	// Update the normal item's URL and host to the mock server
	u, _ := url.Parse(ts.URL)
	_, _ = db.Exec("UPDATE items SET url = ?, host = ? WHERE item_id = 'norm1'", ts.URL, u.Host)
	_, _ = db.Exec("INSERT INTO hosts (host, care) VALUES (?, 'normal') ON CONFLICT(host) DO NOTHING", u.Host)

	var stdout, stderr bytes.Buffer
	app := &application{
		stdout: &stdout,
		stderr: &stderr,
		now:    func() time.Time { return now },
		lookupEnv: func(k string) (string, bool) {
			if k == "LINKDING_URL" {
				return "http://mock-linkding", true
			}
			if k == "LINKDING_TOKEN" {
				return "token", true
			}
			return "", false
		},
	}

	opts := scrapeOptions{
		dbPath:     dbPath,
		timebox:    10 * time.Second,
		normalCap:  10,
		carefulCap: 10,
		rootTag:    "wishlist",
		cdpURL:     "", // NO CDP URL -> Amazon must be skipped
		dryRun:     false,
	}

	// When calling scrape directly without re-seeding mock linkding:
	// Let's test the scrape loop behavior
	var attempted, priced int
	var carefulDone, normalDone int

	deadline := time.Now().Add(opts.timebox)
	for {
		if time.Now().After(deadline) {
			break
		}
		exclude := map[store.Care]bool{}
		if carefulDone >= opts.carefulCap || strings.TrimSpace(opts.cdpURL) == "" {
			exclude[store.Careful] = true
		}
		if normalDone >= opts.normalCap {
			exclude[store.Normal] = true
		}
		if len(exclude) == 2 {
			break
		}

		batch, err := store.NextBatch(db, now.Add(time.Second), 1, exclude)
		if err != nil {
			t.Fatalf("NextBatch: %v", err)
		}
		if len(batch) == 0 {
			break
		}
		entry := batch[0]
		if entry.Care == store.Careful {
			t.Fatalf("expected careful items to be excluded when cdpURL is empty, but got %v", entry)
		}

		outcome, obs := app.attempt(context.Background(), entry, opts.cdpURL)
		attempted++
		if outcome == store.OutcomePriced {
			priced++
		}
		normalDone++
		if err := store.Record(db, obs, outcome, now); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	if attempted != 1 {
		t.Fatalf("attempted = %d, want 1", attempted)
	}
	if priced != 1 {
		t.Fatalf("priced = %d, want 1", priced)
	}

	// Confirm amazon item remains untouched/fresh in DB
	var point string
	err = db.QueryRow("SELECT point FROM items WHERE item_id = 'amz1'").Scan(&point)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if point != "fresh" {
		t.Errorf("amazon item point = %q, want 'fresh'", point)
	}
}

func TestScrapeCarefulIncludedWithCDPURL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wishlist.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	items := []store.Item{
		{
			ItemID:  "amz1",
			URL:     "https://www.amazon.com/dp/B000000001",
			Host:    "amazon.com",
			AddedAt: now,
			Care:    store.Careful,
		},
	}
	if _, err := store.Ingest(db, items, now); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	exclude := map[store.Care]bool{}
	// When cdpURL is provided and carefulCap > 0, careful is NOT excluded
	cdpURL := "http://mock-cdp:9222"
	carefulDone := 0
	carefulCap := 5
	if carefulDone >= carefulCap || strings.TrimSpace(cdpURL) == "" {
		exclude[store.Careful] = true
	}

	batch, err := store.NextBatch(db, now.Add(time.Second), 1, exclude)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if len(batch) != 1 {
		t.Fatalf("expected 1 careful item in batch, got %d", len(batch))
	}
	if batch[0].ItemID != "amz1" {
		t.Errorf("batch item = %s, want 'amz1'", batch[0].ItemID)
	}
	if batch[0].Care != store.Careful {
		t.Errorf("batch item care = %s, want 'careful'", batch[0].Care)
	}
}
