package store

import (
	"database/sql"
	"fmt"
	"time"
)

// tinyTime is the canonical on-disk timestamp format. Fixed-width UTC keeps
// lexicographic comparison in SQL equal to chronological comparison, which the
// queue's ORDER BY depends on.
const tinyTime = "2006-01-02T15:04:05Z"

func stamp(t time.Time) string { return t.UTC().Format(tinyTime) }

func parseStamp(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(tinyTime, s)
}

// Ingest records items from Linkding and enqueues anything new.
//
// Existing rows keep their queue state, so re-running ingest after a build never
// resets the backfill's progress. A new item arrives as PointFresh with
// next_try_at in the past, which is what makes it jump ahead of the historic
// backfill: the queue orders by added_at descending among available items.
func Ingest(db *sql.DB, items []Item, now time.Time) (inserted int, err error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO items (item_id, url, host, care, added_at, point, next_try_at)
		VALUES (?, ?, ?, ?, ?, 'fresh', '1970-01-01T00:00:00Z')
		ON CONFLICT(item_id) DO UPDATE SET
			url = excluded.url,
			host = excluded.host,
			care = excluded.care,
			added_at = excluded.added_at
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, it := range items {
		care := it.Care
		if care == "" {
			care = Normal
		}
		res, err := stmt.Exec(it.ItemID, it.URL, it.Host, string(care), stamp(it.AddedAt))
		if err != nil {
			return 0, fmt.Errorf("ingesting %s: %w", it.ItemID, err)
		}
		// RowsAffected counts updates too, so the caller's "new items" figure
		// comes from a separate check rather than this number.
		_ = res
	}

	if err := ensureHosts(tx, items); err != nil {
		return 0, err
	}

	var before int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&before); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return before, nil
}

// ensureHosts creates host rows for any host seen in items, preserving existing
// politeness state.
func ensureHosts(tx *sql.Tx, items []Item) error {
	stmt, err := tx.Prepare(`
		INSERT INTO hosts (host, care) VALUES (?, ?)
		ON CONFLICT(host) DO UPDATE SET care = excluded.care
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.Host] {
			continue
		}
		seen[it.Host] = true
		care := it.Care
		if care == "" {
			care = Normal
		}
		if _, err := stmt.Exec(it.Host, string(care)); err != nil {
			return err
		}
	}
	return nil
}

// NextBatch returns up to limit queue entries the scraper may attempt now.
//
// Ordering is the whole point of this query:
//
//  1. Careful hosts first as a class, because they are the ones with a hard
//     per-run cap; starving them behind a long normal queue would push the
//     Amazon backfill out indefinitely.
//  2. Newest items first within a care class, so items added today get a price
//     before a 2008 want does. This is the "favour newest, backfill history"
//     rule expressed as an ORDER BY rather than as bookkeeping.
//  3. Never-attempted items before retries, so the queue makes forward progress
//     instead of cycling on failures.
//
// Hosts on cooldown or halted are excluded entirely. A host-level block means
// every item on it waits, not just the one that triggered it.
func NextBatch(db *sql.DB, now time.Time, limit int, exclude map[Care]bool) ([]QueueEntry, error) {
	// The exclusion is a plain filter rather than a per-tier LIMIT because a
	// tier's cap is the caller's concern, not the queue's: the queue's job is
	// "what is eligible", and "what am I allowed to attempt" is policy.
	notCareful := "1"
	if exclude[Careful] {
		notCareful = "0"
	}
	notNormal := "1"
	if exclude[Normal] {
		notNormal = "0"
	}

	rows, err := db.Query(`
		SELECT i.item_id, i.url, i.host, i.care
		FROM items i
		JOIN hosts h ON h.host = i.host
		WHERE h.halted = 0
		  AND (h.cooldown_until IS NULL OR h.cooldown_until <= ?)
		  AND i.next_try_at <= ?
		  AND i.point IN ('fresh', 'failed', 'done', 'blocked')
		  AND (? OR i.care <> 'careful')
		  AND (? OR i.care <> 'normal')
		ORDER BY
			CASE i.care WHEN 'careful' THEN 0 ELSE 1 END,
			i.point = 'fresh' DESC,
			i.added_at DESC,
			i.item_id
		LIMIT ?
	`, stamp(now), stamp(now), notCareful, notNormal, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QueueEntry
	for rows.Next() {
		var e QueueEntry
		var care string
		if err := rows.Scan(&e.ItemID, &e.URL, &e.Host, &care); err != nil {
			return nil, err
		}
		e.Care = Care(care)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DueAt reports the earliest moment any item becomes eligible, so a caller can
// say "nothing to do until 14:20" instead of "nothing to do".
func DueAt(db *sql.DB) (time.Time, bool, error) {
	var s sql.NullString
	err := db.QueryRow(`
		SELECT MIN(MAX(i.next_try_at, COALESCE(h.cooldown_until, i.next_try_at)))
		FROM items i JOIN hosts h ON h.host = i.host
		WHERE h.halted = 0
	`).Scan(&s)
	if err != nil {
		return time.Time{}, false, err
	}
	if !s.Valid || s.String == "" {
		return time.Time{}, false, nil
	}
	t, err := parseStamp(s.String)
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
}

// Progress summarises the queue for the run report.
type Progress struct {
	Total   int
	Fresh   int
	Done    int
	Failed  int
	Blocked int
	Halted  int
	Stale   int
}

// Summarise counts item states, plus hosts currently halted.
func Summarise(db *sql.DB, staleAfter time.Duration, now time.Time) (Progress, error) {
	var p Progress
	rows, err := db.Query(`SELECT point, COUNT(*) FROM items GROUP BY point`)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var point string
		var n int
		if err := rows.Scan(&point, &n); err != nil {
			return p, err
		}
		p.Total += n
		switch Point(point) {
		case PointFresh:
			p.Fresh = n
		case PointDone:
			p.Done = n
		case PointFailed:
			p.Failed = n
		case PointBlocked:
			p.Blocked = n
		}
	}
	if err := rows.Err(); err != nil {
		return p, err
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE halted = 1`).Scan(&p.Halted); err != nil {
		return p, err
	}

	cutoff := stamp(now.Add(-staleAfter))
	err = db.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT i.item_id FROM items i
			LEFT JOIN (SELECT item_id, MAX(observed_at) AS seen FROM observations GROUP BY item_id) o
				ON o.item_id = i.item_id
			WHERE o.seen IS NULL OR o.seen < ?
		)
	`, cutoff).Scan(&p.Stale)
	if err != nil {
		return p, err
	}
	return p, nil
}
