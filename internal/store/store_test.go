package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func seed(t *testing.T, db *sql.DB, items []Item) {
	t.Helper()
	if _, err := Ingest(db, items, at("2026-11-24T00:00:00Z")); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

func TestNextBatchFavoursNewestWithinCare(t *testing.T) {
	db := openTemp(t)
	seed(t, db, []Item{
		{ItemID: "old", URL: "https://shop.example/a", Host: "shop.example", AddedAt: at("2009-01-01T00:00:00Z")},
		{ItemID: "new", URL: "https://shop.example/b", Host: "shop.example", AddedAt: at("2026-11-01T00:00:00Z")},
		{ItemID: "mid", URL: "https://shop.example/c", Host: "shop.example", AddedAt: at("2020-06-01T00:00:00Z")},
	})

	got, err := NextBatch(db, at("2026-11-24T00:00:00Z"), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	want := []string{"new", "mid", "old"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ItemID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].ItemID, id)
		}
	}
}

func TestNextBatchPutsCarefulHostsFirst(t *testing.T) {
	db := openTemp(t)
	seed(t, db, []Item{
		{ItemID: "shop-new", URL: "https://shop.example/a", Host: "shop.example",
			Care: Normal, AddedAt: at("2026-11-20T00:00:00Z")},
		{ItemID: "amazon-old", URL: "https://amazon.com/dp/X", Host: "amazon.com",
			Care: Careful, AddedAt: at("2010-01-01T00:00:00Z")},
	})

	got, err := NextBatch(db, at("2026-11-24T00:00:00Z"), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	// Careful first as a class even though the item is far older, because the
	// careful tier has a per-run cap and would otherwise be starved.
	if got[0].ItemID != "amazon-old" {
		t.Fatalf("first entry = %q, want the careful host's item", got[0].ItemID)
	}
}

func TestBlockIsHostWideAndEscalates(t *testing.T) {
	db := openTemp(t)
	now := at("2026-11-24T12:00:00Z")
	seed(t, db, []Item{
		{ItemID: "a", URL: "https://shop.example/a", Host: "shop.example", AddedAt: at("2026-11-01T00:00:00Z")},
		{ItemID: "b", URL: "https://shop.example/b", Host: "shop.example", AddedAt: at("2026-11-02T00:00:00Z")},
	})

	if err := Record(db, Observation{ItemID: "a", ObservedAt: now, Source: "jsonld"}, OutcomeBlocked, now); err != nil {
		t.Fatalf("Record blocked: %v", err)
	}

	// The sibling must wait too, even though it was never attempted.
	got, err := NextBatch(db, now.Add(time.Hour), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	for _, e := range got {
		if e.ItemID == "b" {
			t.Fatal("sibling item was still eligible during the host's cooldown")
		}
	}

	// After the first cooldown passes, work resumes.
	got, err = NextBatch(db, now.Add(FirstBlockCooldown+time.Minute), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch after cooldown: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected work to resume after the first cooldown")
	}
}

func TestThirdBlockHaltsHost(t *testing.T) {
	db := openTemp(t)
	seed(t, db, []Item{
		{ItemID: "a", URL: "https://shop.example/a", Host: "shop.example", AddedAt: at("2026-11-01T00:00:00Z")},
	})

	now := at("2026-11-24T12:00:00Z")
	for i := 0; i < MaxBlocksBeforeHalt; i++ {
		if err := Record(db, Observation{ItemID: "a", ObservedAt: now, Source: "jsonld"}, OutcomeBlocked, now); err != nil {
			t.Fatalf("Record blocked %d: %v", i, err)
		}
		now = now.Add(time.Hour)
	}

	got, err := NextBatch(db, now.Add(30*24*time.Hour), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("halted host still yielded %d entries", len(got))
	}

	// A human re-enables deliberately; nothing resumes on its own.
	if err := SetHalted(db, "shop.example", false); err != nil {
		t.Fatalf("SetHalted: %v", err)
	}
	got, err = NextBatch(db, now.Add(30*24*time.Hour), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch after resume: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected work after a human resumed the host")
	}
}

func TestNoDataIsNotABlock(t *testing.T) {
	db := openTemp(t)
	now := at("2026-11-24T12:00:00Z")
	seed(t, db, []Item{
		{ItemID: "a", URL: "https://shop.example/a", Host: "shop.example", AddedAt: at("2026-11-01T00:00:00Z")},
	})

	if err := Record(db, Observation{ItemID: "a", ObservedAt: now, Source: "jsonld", Status: "no_data"}, OutcomeNoData, now); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var halted int
	if err := db.QueryRow(`SELECT halted FROM hosts WHERE host = 'shop.example'`).Scan(&halted); err != nil {
		t.Fatalf("reading host: %v", err)
	}
	if halted != 0 {
		t.Fatal("a store with no price must not be treated as blocked")
	}
}

func TestIngestPreservesExistingQueueState(t *testing.T) {
	db := openTemp(t)
	item := Item{ItemID: "a", URL: "https://shop.example/a", Host: "shop.example", AddedAt: at("2026-11-01T00:00:00Z")}
	now := at("2026-11-24T12:00:00Z")
	seed(t, db, []Item{item})

	if err := Record(db, Observation{ItemID: "a", ObservedAt: now, Source: "jsonld"}, OutcomePriced, now); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Re-ingesting after a build must not reset progress to fresh.
	seed(t, db, []Item{item})

	var point string
	if err := db.QueryRow(`SELECT point FROM items WHERE item_id='a'`).Scan(&point); err != nil {
		t.Fatalf("reading point: %v", err)
	}
	if point != string(PointDone) {
		t.Fatalf("point = %q after re-ingest, want %q", point, PointDone)
	}
}

func TestDeadIsPermanentAndNotAStaleItem(t *testing.T) {
	db := openTemp(t)
	now := at("2026-11-24T12:00:00Z")
	seed(t, db, []Item{
		{ItemID: "gone", URL: "https://closed.example/a", Host: "closed.example", AddedAt: at("2009-01-01T00:00:00Z")},
		{ItemID: "live", URL: "https://open.example/b", Host: "open.example", AddedAt: at("2026-11-01T00:00:00Z")},
	})

	if err := Record(db, Observation{ItemID: "gone", ObservedAt: now, Source: "none", Status: "dead", Detail: "http 404"}, OutcomeDead, now); err != nil {
		t.Fatalf("Record dead: %v", err)
	}

	// A dead item must never come back, even a century later.
	got, err := NextBatch(db, now.Add(50*365*24*time.Hour), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	for _, e := range got {
		if e.ItemID == "gone" {
			t.Fatal("a dead item was re-queued")
		}
	}
	if len(got) != 1 || got[0].ItemID != "live" {
		t.Fatalf("expected only the live item, got %v", got)
	}

	// The row survives so the item still renders on the site.
	var point string
	if err := db.QueryRow(`SELECT point FROM items WHERE item_id='gone'`).Scan(&point); err != nil {
		t.Fatalf("reading dead item: %v", err)
	}
	if point != string(PointDead) {
		t.Fatalf("point = %q, want %q", point, PointDead)
	}

	p, err := Summarise(db, RefreshAfter, now)
	if err != nil {
		t.Fatalf("Summarise: %v", err)
	}
	if p.Dead != 1 {
		t.Errorf("Dead = %d, want 1", p.Dead)
	}
	if p.Failed != 0 {
		t.Errorf("Failed = %d, want 0 — a 404 must not be counted as a retryable failure", p.Failed)
	}
	if p.Stale != 1 {
		t.Errorf("Stale = %d, want 1 (only the live, never-observed item)", p.Stale)
	}
}

func TestUnavailableIsRetriedButNeverADeadEnd(t *testing.T) {
	db := openTemp(t)
	now := at("2026-11-24T12:00:00Z")
	seed(t, db, []Item{
		{ItemID: "soldout", URL: "https://shop.example/a", Host: "shop.example", AddedAt: at("2026-11-01T00:00:00Z")},
	})

	err := Record(db, Observation{
		ItemID: "soldout", ObservedAt: now, Source: "jsonld",
		Status: "unavailable", Detail: "OutOfStock",
	}, OutcomeUnavailable, now)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Restocking is real, so an unavailable item must come back eventually.
	soon, err := NextBatch(db, now.Add(time.Hour), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch: %v", err)
	}
	if len(soon) != 0 {
		t.Fatal("an unavailable item was retried immediately")
	}

	later, err := NextBatch(db, now.Add(RefreshAfter+time.Hour), 10, nil)
	if err != nil {
		t.Fatalf("NextBatch later: %v", err)
	}
	if len(later) != 1 {
		t.Fatalf("expected the item to be retried after the refresh interval, got %d", len(later))
	}

	p, err := Summarise(db, RefreshAfter, now)
	if err != nil {
		t.Fatalf("Summarise: %v", err)
	}
	if p.Unavailable != 1 {
		t.Errorf("Unavailable = %d, want 1", p.Unavailable)
	}
	if p.Dead != 0 {
		t.Errorf("Dead = %d, want 0 — unavailable is not the same as gone", p.Dead)
	}
}
