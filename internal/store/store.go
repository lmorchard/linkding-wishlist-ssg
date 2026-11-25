// Package store holds scraped observations in a local SQLite database.
//
// Linkding stays read-only. Everything this package learns about a wishlist item
// — prices, availability, and the scraping queue's own progress — lives here, so
// a scraper bug can never damage a bookmark. The shape is deliberately the same
// as linkding-to-opml's local database: upstream is the source of items, local
// state is the source of everything derived.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Point is the lifecycle position of a queued item.
type Point string

const (
	// PointFresh has never been attempted.
	PointFresh Point = "fresh"
	// PointDone has a successful observation.
	PointDone Point = "done"
	// PointFailed attempted and got no usable data; retried on a long interval.
	PointFailed Point = "failed"
	// PointBlocked means the host refused us. Distinct from PointFailed on
	// purpose: "there is no price here" and "we were turned away" demand
	// different responses, and conflating them hides a captcha inside a
	// report that looks like a working store.
	PointBlocked Point = "blocked"
)

// Care is how much caution a host's requests deserve. It is a property of the
// store, not of the item, because being blocked is a property of the host.
type Care string

const (
	// Careful hosts (Amazon) get a fixed small number of attempts per run and
	// a hard stop on any sign of trouble.
	Careful Care = "careful"
	// Normal hosts get whatever fits in the timebox and a gentler backoff.
	Normal Care = "normal"
)

// Observation is one price reading for an item.
//
// Source records which extraction rung answered, so the report can say why an
// item has no price rather than merely that it does not. An empty Price with
// Source "jsonld" is a real answer: the page had structured data and it carried
// no offer.
type Observation struct {
	ItemID     string
	ObservedAt time.Time
	Price      *float64
	Currency   string
	Available  *bool
	Source     string
	Status     string
	Detail     string
}

// Item is a scrape target and its queue state.
type Item struct {
	ItemID    string
	URL       string
	Host      string
	Care      Care
	AddedAt   time.Time
	Point     Point
	Attempts  int
	LastTryAt *time.Time
	NextTryAt time.Time
}

// QueueEntry is one unit of work handed to the scraper.
type QueueEntry struct {
	ItemID string
	URL    string
	Host   string
	Care   Care
}

const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

-- One row per wishlist item. Populated at ingest from Linkding's read-only
-- view of the world; never updated from a scrape.
CREATE TABLE IF NOT EXISTS items (
	item_id   TEXT PRIMARY KEY,
	url       TEXT NOT NULL,
	host      TEXT NOT NULL,
	care      TEXT NOT NULL DEFAULT 'normal',
	added_at  TEXT NOT NULL,
	point     TEXT NOT NULL DEFAULT 'fresh',
	attempts  INTEGER NOT NULL DEFAULT 0,
	last_try_at TEXT,
	next_try_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00Z'
);

CREATE INDEX IF NOT EXISTS items_queue_order
	ON items (point, care, added_at DESC);

CREATE INDEX IF NOT EXISTS items_host
	ON items (host);

-- Every reading ever taken. Append-only: the newest row per item wins.
CREATE TABLE IF NOT EXISTS observations (
	item_id     TEXT NOT NULL,
	observed_at TEXT NOT NULL,
	price       REAL,
	currency    TEXT,
	available   INTEGER,
	source      TEXT NOT NULL,
	status      TEXT NOT NULL,
	detail      TEXT,
	PRIMARY KEY (item_id, observed_at)
);

-- Per-host politeness state. A block is a property of the host, so backing off
-- one item at a time would let 1019 siblings compound the offence.
CREATE TABLE IF NOT EXISTS hosts (
	host        TEXT PRIMARY KEY,
	care        TEXT NOT NULL DEFAULT 'normal',
	cooldown_until TEXT,
	blocks      INTEGER NOT NULL DEFAULT 0,
	last_block_at TEXT,
	halted      INTEGER NOT NULL DEFAULT 0
);
`

// Open opens or creates the database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	// SQLite tolerates exactly one writer. Multiple connections in one process
	// turn that into SQLITE_BUSY errors under the hourly cron overlapping a
	// long run, so the writer set stays deliberately small.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	return db, nil
}
