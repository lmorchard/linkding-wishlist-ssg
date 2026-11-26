package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Block cooldown ladder. Escalation is deliberately severe: the first block is
// a warning, the second means the policy is wrong, and the third means we stop
// and ask a human. Nothing here resumes automatically after that.
const (
	FirstBlockCooldown  = 24 * time.Hour
	SecondBlockCooldown = 7 * 24 * time.Hour
	// SecondBlockWindow is how recently a block must have happened for the
	// second one to count as a repeat rather than a fresh incident.
	SecondBlockWindow = 7 * 24 * time.Hour
	// MaxBlocksBeforeHalt is the block count that halts a host.
	MaxBlocksBeforeHalt = 3

	// RetryAfterNoData is how long a "page had no price" result waits. Long,
	// because a store that does not publish prices today probably will not
	// tomorrow either, and this is the bulk of the long tail.
	RetryAfterNoData = 7 * 24 * time.Hour
	// RetryAfterError is for transient failures (timeouts, 5xx).
	RetryAfterError = 6 * time.Hour
	// RefreshAfter is how stale a successful observation may get before the
	// item is worth revisiting.
	RefreshAfter = 7 * 24 * time.Hour
)

// Outcome says what a scrape attempt learned. The distinction between NoData
// and Blocked is the one that matters: NoData is a working store with nothing
// to say, Blocked is a store refusing to talk, and the queue responds to them
// very differently.
type Outcome string

const (
	OutcomePriced  Outcome = "priced"
	OutcomeNoData  Outcome = "no_data"
	OutcomeBlocked Outcome = "blocked"
	OutcomeError   Outcome = "error"
	// OutcomeDead is a permanent answer: the page returned 404 (or 410).
	// Recorded separately from OutcomeError so a closed shop stops consuming
	// queue slots while a transient timeout keeps retrying.
	OutcomeDead Outcome = "dead"
	// OutcomeUnavailable means the page answered but the shop says the thing
	// cannot be bought. It still gets a price recorded when one is known (a
	// sold-out item often shows its last price), but it leaves the live list.
	OutcomeUnavailable Outcome = "unavailable"
)

// Record appends an observation and advances the item's queue state.
func Record(db *sql.DB, obs Observation, outcome Outcome, now time.Time) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := appendObservation(tx, obs); err != nil {
		return err
	}

	var next time.Time
	var point Point
	switch outcome {
	case OutcomePriced:
		point, next = PointDone, now.Add(RefreshAfter)
	case OutcomeNoData:
		point, next = PointFailed, now.Add(RetryAfterNoData)
	case OutcomeError:
		point, next = PointFailed, now.Add(RetryAfterError)
	case OutcomeBlocked:
		point, next = PointBlocked, now.Add(FirstBlockCooldown)
	case OutcomeDead:
		// Never retried. A far-future next_try_at keeps the item out of every
		// query without needing a special case in NextBatch, and leaves the row
		// intact so the item still renders on the site.
		point, next = PointDead, now.Add(100*365*24*time.Hour)
	case OutcomeUnavailable:
		// Unlike dead, this can change: shops restock. Retried on the long
		// refresh interval so a returning item is noticed without hammering a
		// shop that is plainly not selling it.
		point, next = PointUnavailable, now.Add(RefreshAfter)
	default:
		return fmt.Errorf("unknown outcome %q", outcome)
	}

	_, err = tx.Exec(`
		UPDATE items
		SET point = ?, attempts = attempts + 1,
		    last_try_at = ?, next_try_at = ?
		WHERE item_id = ?
	`, string(point), stamp(now), stamp(next), obs.ItemID)
	if err != nil {
		return fmt.Errorf("updating %s: %w", obs.ItemID, err)
	}

	if outcome == OutcomeBlocked {
		if err := blockHost(tx, obs.ItemID, now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func appendObservation(tx *sql.Tx, obs Observation) error {
	var available any
	if obs.Available != nil {
		if *obs.Available {
			available = 1
		} else {
			available = 0
		}
	}
	_, err := tx.Exec(`
		INSERT INTO observations
			(item_id, observed_at, price, currency, available, source, status, detail, image_url, description, raw_payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(item_id, observed_at) DO UPDATE SET
			price = excluded.price,
			currency = excluded.currency,
			available = excluded.available,
			source = excluded.source,
			status = excluded.status,
			detail = excluded.detail,
			image_url = excluded.image_url,
			description = excluded.description,
			raw_payload = excluded.raw_payload
	`, obs.ItemID, stamp(obs.ObservedAt), obs.Price, obs.Currency,
		available, obs.Source, obs.Status, obs.Detail, obs.ImageURL, obs.Description, obs.RawPayload)
	if err != nil {
		return fmt.Errorf("recording observation for %s: %w", obs.ItemID, err)
	}
	return nil
}

// blockHost applies the cooldown ladder to the host owning itemID.
//
// Blocks land on the host, never the item. If Amazon shows a verify page, every
// Amazon item waits — otherwise 1019 siblings retry into the same wall and turn
// one warning into a pattern.
func blockHost(tx *sql.Tx, itemID string, now time.Time) error {
	var host string
	err := tx.QueryRow(`SELECT host FROM items WHERE item_id = ?`, itemID).Scan(&host)
	if err != nil {
		return fmt.Errorf("finding host for %s: %w", itemID, err)
	}

	var cooldown string
	var lastBlock sql.NullString
	err = tx.QueryRow(`
		SELECT COALESCE(cooldown_until, ''), last_block_at FROM hosts WHERE host = ?
	`, host).Scan(&cooldown, &lastBlock)
	if err != nil {
		return fmt.Errorf("reading host %s: %w", host, err)
	}

	repeat := false
	if lastBlock.Valid && lastBlock.String != "" {
		if prev, perr := parseStamp(lastBlock.String); perr == nil {
			repeat = now.Sub(prev) <= SecondBlockWindow
		}
	}

	next := now.Add(FirstBlockCooldown)
	if repeat {
		next = now.Add(SecondBlockCooldown)
	}

	_, err = tx.Exec(`
		UPDATE hosts
		SET blocks = blocks + 1,
		    last_block_at = ?,
		    cooldown_until = ?,
		    halted = CASE WHEN blocks + 1 >= ? THEN 1 ELSE halted END
		WHERE host = ?
	`, stamp(now), stamp(next), MaxBlocksBeforeHalt, host)
	if err != nil {
		return fmt.Errorf("escalating host %s: %w", host, err)
	}

	// During a host cooldown, every queued item on that host waits with it.
	_, err = tx.Exec(`
		UPDATE items
		SET next_try_at = MAX(next_try_at, ?)
		WHERE host = ?
	`, stamp(next), host)
	return err
}

// SetHalted is the kill switch. Halting a host stops the queue consulting it
// entirely; resuming is a deliberate human act, never automatic.
func SetHalted(db *sql.DB, host string, halted bool) error {
	_, err := db.Exec(`UPDATE hosts SET halted = ? WHERE host = ?`, boolInt(halted), host)
	return err
}

// HaltAll halts every host. The blunt instrument for "something is wrong".
func HaltAll(db *sql.DB, halted bool) error {
	_, err := db.Exec(`UPDATE hosts SET halted = ?`, boolInt(halted))
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// LatestObservation returns the newest reading for an item, if any.
func LatestObservation(db *sql.DB, itemID string) (Observation, bool, error) {
	var obs Observation
	var observed string
	var price sql.NullFloat64
	var currency, source, status, detail, imageURL, description, rawPayload sql.NullString
	var available sql.NullInt64

	err := db.QueryRow(`
		SELECT observed_at, price, currency, available, source, status, detail,
		       COALESCE(image_url, ''), COALESCE(description, ''), COALESCE(raw_payload, '')
		FROM observations WHERE item_id = ?
		ORDER BY observed_at DESC LIMIT 1
	`, itemID).Scan(&observed, &price, &currency, &available, &source, &status, &detail,
		&imageURL, &description, &rawPayload)
	if err == sql.ErrNoRows {
		return obs, false, nil
	}
	if err != nil {
		return obs, false, err
	}

	obs.ItemID = itemID
	obs.ObservedAt, _ = parseStamp(observed)
	if price.Valid {
		p := price.Float64
		obs.Price = &p
	}
	obs.Currency = currency.String
	if available.Valid {
		a := available.Int64 == 1
		obs.Available = &a
	}
	obs.Source = source.String
	obs.Status = status.String
	obs.Detail = detail.String
	obs.ImageURL = imageURL.String
	obs.Description = description.String
	obs.RawPayload = rawPayload.String
	return obs, true, nil
}

// HostReport summarises per-host yield, which is the honest measure of the
// scraper's success. Overall coverage is dominated by Amazon's share of the
// item count and says almost nothing about whether the generic pass works.
type HostReport struct {
	Host    string
	Items   int
	Priced  int
	NoData  int
	Blocked int
	Halted  bool
}

// HostReports returns one row per host that has been attempted, best yield
// first. Hosts never attempted are omitted.
func HostReports(db *sql.DB) ([]HostReport, error) {
	rows, err := db.Query(`
		SELECT i.host,
		       COUNT(DISTINCT i.item_id) AS items,
		       COUNT(DISTINCT CASE WHEN o.status = 'priced' THEN o.item_id END) AS priced,
		       COUNT(DISTINCT CASE WHEN o.status = 'no_data' THEN o.item_id END) AS nodata,
		       h.halted
		FROM items i
		JOIN hosts h ON h.host = i.host
		LEFT JOIN observations o ON o.item_id = i.item_id
		WHERE i.attempts > 0
		GROUP BY i.host
		ORDER BY priced * 1.0 / items DESC, items DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HostReport
	for rows.Next() {
		var r HostReport
		var halted int
		if err := rows.Scan(&r.Host, &r.Items, &r.Priced, &r.NoData, &halted); err != nil {
			return nil, err
		}
		r.Halted = halted == 1
		r.Blocked = 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestPrices returns the newest priced observation per item, keyed by item
// ID. Loading every reading in one query beats issuing 1580 single-row lookups
// during a build.
//
// Items whose newest observation carries no price are omitted rather than
// returned as zero: "we checked and there was no price" and "we never checked"
// both mean "show nothing", but conflating them with a real 0.00 would be a lie
// on the page.
func LatestPrices(db *sql.DB) (map[string]Observation, error) {
	rows, err := db.Query(`
		SELECT o.item_id, o.observed_at, o.price, COALESCE(o.currency, ''),
		       o.source, o.status, COALESCE(o.image_url, ''), COALESCE(o.description, '')
		FROM observations o
		JOIN (
			SELECT item_id, MAX(observed_at) AS newest
			FROM observations GROUP BY item_id
		) latest ON latest.item_id = o.item_id AND latest.newest = o.observed_at
		WHERE o.price IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]Observation)
	for rows.Next() {
		var obs Observation
		var observed, currency, source, status, imageURL, description string
		var price sql.NullFloat64
		if err := rows.Scan(&obs.ItemID, &observed, &price, &currency, &source, &status, &imageURL, &description); err != nil {
			return nil, err
		}
		obs.ObservedAt, _ = parseStamp(observed)
		if price.Valid {
			p := price.Float64
			obs.Price = &p
		}
		obs.Currency = currency
		obs.Source = source
		obs.Status = status
		obs.ImageURL = imageURL
		obs.Description = description
		out[obs.ItemID] = obs
	}
	return out, rows.Err()
}

// UnavailableItems returns the set of item IDs whose latest observation says
// the shop cannot sell it.
//
// Only an affirmative structured signal lands here (see extract.parseAvailability).
// A missing price is not unavailability.
func UnavailableItems(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`
		SELECT item_id FROM items WHERE point = 'unavailable'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
