package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/store"
)

func TestExtractReprocessesRawPayloads(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wishlist.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	items := []store.Item{
		{
			ItemID:  "it1",
			URL:     "https://shop.example/widget",
			Host:    "shop.example",
			AddedAt: now,
			Care:    store.Normal,
		},
	}
	if _, err := store.Ingest(db, items, now); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Record an observation with raw JSON-LD payload, but where earlier extraction
	// missed image and description (simulating older scraper code)
	rawHTML := `<!doctype html><html><head>
		<script type="application/ld+json">
		{
			"@type": "Product",
			"name": "Super Widget",
			"description": "Reprocessed description",
			"image": "https://shop.example/widget.jpg",
			"offers": {
				"price": "39.99",
				"priceCurrency": "USD",
				"availability": "https://schema.org/InStock"
			}
		}
		</script></head><body><h1>Widget</h1></body></html>`

	initialPrice := 39.99
	avail := true
	obs := store.Observation{
		ItemID:      "it1",
		ObservedAt:  now,
		Price:       &initialPrice,
		Currency:    "USD",
		Available:   &avail,
		Source:      "jsonld",
		Status:      "priced",
		Detail:      "",
		ImageURL:    "", // missing!
		Description: "", // missing!
		RawPayload:  rawHTML,
	}
	if err := store.Record(db, obs, store.OutcomePriced, now); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := &application{
		stdout:    &stdout,
		stderr:    &stderr,
		lookupEnv: os.LookupEnv,
		now:       func() time.Time { return now },
	}

	exit := app.runExtract(context.Background(), []string{"--store", dbPath})
	if exit != 0 {
		t.Fatalf("runExtract exit code = %d, stderr: %s", exit, stderr.String())
	}

	if !strings.Contains(stdout.String(), "reprocessing 1 stored observation payload") {
		t.Errorf("stdout missing reprocess notice: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "updated: 1") {
		t.Errorf("stdout missing updated count: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "new images: 1") {
		t.Errorf("stdout missing new images count: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "new descriptions: 1") {
		t.Errorf("stdout missing new descriptions count: %s", stdout.String())
	}

	// Verify database was updated in-place
	got, ok, err := store.LatestObservation(db, "it1")
	if err != nil {
		t.Fatalf("LatestObservation: %v", err)
	}
	if !ok {
		t.Fatal("expected observation to exist")
	}
	if got.ImageURL != "https://shop.example/widget.jpg" {
		t.Errorf("ImageURL = %q, want 'https://shop.example/widget.jpg'", got.ImageURL)
	}
	if got.Description != "Reprocessed description" {
		t.Errorf("Description = %q, want 'Reprocessed description'", got.Description)
	}
}

func TestExtractDryRunDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wishlist.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	items := []store.Item{
		{ItemID: "it2", URL: "https://shop.example/dry", Host: "shop.example", AddedAt: now, Care: store.Normal},
	}
	if _, err := store.Ingest(db, items, now); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	rawHTML := `<!doctype html><html><head>
		<script type="application/ld+json">
		{"@type": "Product", "image": "https://shop.example/dry.jpg", "offers": {"price": "10.00", "priceCurrency": "USD"}}
		</script></head></html>`

	obs := store.Observation{
		ItemID:      "it2",
		ObservedAt:  now,
		Source:      "jsonld",
		Status:      "no_data",
		ImageURL:    "",
		Description: "",
		RawPayload:  rawHTML,
	}
	if err := store.Record(db, obs, store.OutcomeNoData, now); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := &application{
		stdout:    &stdout,
		stderr:    &stderr,
		lookupEnv: os.LookupEnv,
		now:       func() time.Time { return now },
	}

	exit := app.runExtract(context.Background(), []string{"--store", dbPath, "--dry-run"})
	if exit != 0 {
		t.Fatalf("runExtract exit = %d, stderr: %s", exit, stderr.String())
	}

	// Verify database was NOT changed
	got, ok, err := store.LatestObservation(db, "it2")
	if err != nil || !ok {
		t.Fatalf("LatestObservation: %v", err)
	}
	if got.ImageURL != "" {
		t.Errorf("ImageURL was mutated during dry-run: %q", got.ImageURL)
	}
	if got.Price != nil {
		t.Errorf("Price was mutated during dry-run: %v", got.Price)
	}
}
