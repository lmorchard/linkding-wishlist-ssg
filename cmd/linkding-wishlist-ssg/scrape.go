package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/config"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/extract"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/fetch"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/identity"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/store"
)

// DefaultTimebox leaves 15 minutes of an hourly cron slot for the run's own
// overhead: finishing an in-flight request, writing state, and exiting before
// the next tick starts.
const DefaultTimebox = 45 * time.Minute

// scratchScrape is the application's test seam for the scrape command, mirroring
// how build is exercised.
type scrapeOptions struct {
	dbPath     string
	timebox    time.Duration
	normalCap  int
	carefulCap int
	rootTag    string
	dryRun     bool
}

func (a *application) runScrape(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("scrape", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: linkding-wishlist-ssg scrape [flags]

Fetches prices for queued wishlist items and records them in a local SQLite
store. Linkding is read-only: nothing here writes back to it.

Flags:
`)
		fs.PrintDefaults()
	}

	fs.String("store", "./wishlist.db", "path to the local observation store")
	fs.Duration("timebox", DefaultTimebox, "wall-clock budget for this run")
	fs.Int("normal-cap", 200, "maximum normal-tier items attempted per run")
	fs.Int("careful-cap", 3, "maximum careful-tier (Amazon) items attempted per run")
	fs.String("tag", "wishlist", "root selector tag used when seeding the queue")
	fs.Bool("dry-run", false, "list what would be attempted without fetching")

	if wantsHelp(args) {
		fs.SetOutput(a.stdout)
		fs.Usage()
		return 0
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(a.stderr, "error: unexpected argument %q\n", fs.Arg(0))
		return 1
	}

	envFile := map[string]string{}
	if a.envFilePath != "" {
		if loaded, err := config.LoadEnv(a.envFilePath); err == nil {
			envFile = loaded
		}
	}
	resolve := siteconfig.NewResolver(fs, a.lookupEnv, envFile, func(msg string) {
		fmt.Fprintf(a.stderr, "warning: %s\n", msg)
	})

	opts := scrapeOptions{
		dbPath:     resolve.String("store"),
		timebox:    durationOrDefault(resolve.String("timebox"), DefaultTimebox),
		normalCap:  resolve.Int("normal-cap"),
		carefulCap: resolve.Int("careful-cap"),
		rootTag:    resolve.String("tag"),
		dryRun:     resolve.Bool("dry-run"),
	}
	if opts.normalCap < 0 || opts.carefulCap < 0 {
		fmt.Fprintln(a.stderr, "error: caps must not be negative")
		return 1
	}

	return a.scrape(ctx, opts, envFile)
}

func (a *application) scrape(ctx context.Context, opts scrapeOptions, envFile map[string]string) int {
	db, err := store.Open(opts.dbPath)
	if err != nil {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	now := a.now()

	// Seed the queue from Linkding, which stays read-only. Ingest is idempotent
	// and preserves queue progress, so this runs on every invocation.
	if err := a.seedQueue(ctx, db, opts, envFile, now); err != nil {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		return 1
	}

	deadline := now.Add(opts.timebox)
	var attempted, priced, noData, blocked, errored int
	var carefulDone, normalDone int
	start := time.Now()

	for {
		if time.Now().After(deadline) {
			fmt.Fprintf(a.stdout, "\ntimebox reached after %s\n", opts.timebox)
			break
		}
		// A tier whose cap is exhausted is excluded from the query rather than
		// skipped in the loop. Careful hosts sort first, so a loop-level skip would
		// break out before ever reaching the normal tier — "skip Amazon" would
		// silently mean "do nothing".
		exclude := map[store.Care]bool{}
		if carefulDone >= opts.carefulCap {
			exclude[store.Careful] = true
		}
		if normalDone >= opts.normalCap {
			exclude[store.Normal] = true
		}
		if len(exclude) == 2 {
			break
		}

		batch, err := store.NextBatch(db, a.now(), 1, exclude)
		if err != nil {
			fmt.Fprintf(a.stderr, "error: %v\n", err)
			return 1
		}
		if len(batch) == 0 {
			break
		}
		entry := batch[0]

		if entry.Care == store.Careful {
			carefulDone++
		} else {
			normalDone++
		}

		if opts.dryRun {
			fmt.Fprintf(a.stdout, "would fetch [%s] %s\n", entry.Care, entry.URL)
			attempted++
			// Advance artificially so the dry run terminates.
			_ = store.Record(db, store.Observation{
				ItemID: entry.ItemID, ObservedAt: a.now(), Source: "dry-run", Status: "no_data",
			}, store.OutcomeNoData, a.now())
			continue
		}

		outcome, obs := a.attempt(ctx, entry)
		attempted++
		switch outcome {
		case store.OutcomePriced:
			priced++
		case store.OutcomeNoData:
			noData++
		case store.OutcomeBlocked:
			blocked++
		case store.OutcomeError:
			errored++
		}

		if err := store.Record(db, obs, outcome, a.now()); err != nil {
			fmt.Fprintf(a.stderr, "error: %v\n", err)
			return 1
		}

		label := "-"
		if obs.Price != nil {
			label = fmt.Sprintf("%.2f %s", *obs.Price, obs.Currency)
		}
		fmt.Fprintf(a.stdout, "%-8s %-28s %10s  %s\n", outcome, trunc(entry.Host, 28), label, trunc(entry.URL, 60))

		if outcome == store.OutcomeBlocked {
			fmt.Fprintf(a.stderr, "\nBLOCKED by %s (%s) — host cooling down, stopping this run\n",
				entry.Host, obs.Detail)
			break
		}
	}

	a.reportScrape(db, start, attempted, priced, noData, blocked, errored)
	return 0
}

// seedQueue pulls bookmarks from Linkding and ingests them, classifying each
// host's care tier.
func (a *application) seedQueue(ctx context.Context, db *sql.DB, opts scrapeOptions, envFile map[string]string, now time.Time) error {
	linkdingURL := strings.TrimSpace(a.envValue(envFile, "LINKDING_URL"))
	linkdingToken := strings.TrimSpace(a.envValue(envFile, "LINKDING_TOKEN"))
	if linkdingURL == "" || linkdingToken == "" {
		return fmt.Errorf("LINKDING_URL and LINKDING_TOKEN are required to seed the queue")
	}

	client, err := linkding.NewClient(linkding.ClientOptions{
		BaseURL: linkdingURL, Token: linkdingToken,
		HTTPClient: a.httpClient, RetryBackoffs: a.retryBackoffs,
	})
	if err != nil {
		return err
	}

	bookmarks, err := client.ListAll(ctx, "#"+opts.rootTag, false, 100, func(fetched, total int) {
		fmt.Fprintf(a.stdout, "  %d/%d bookmarks...\n", fetched, total)
	})
	if err != nil {
		return err
	}

	items := make([]store.Item, 0, len(bookmarks))
	for _, b := range bookmarks {
		host, ok := identity.StoreKey(b.URL)
		if !ok {
			continue
		}
		added := b.DateAdded
		if added.IsZero() {
			added = b.DateCreated
		}
		items = append(items, store.Item{
			ItemID:  identity.ItemID(b.URL),
			URL:     b.URL,
			Host:    host,
			Care:    careFor(host),
			AddedAt: added,
		})
	}

	_, err = store.Ingest(db, items, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "seeded %d items\n", len(items))
	return nil
}

// careFor assigns a politeness tier by host.
//
// Amazon is careful because the downside of being wrong is losing the ability to
// shop, not merely failing to scrape. Everything else is normal.
func careFor(host string) store.Care {
	h := strings.ToLower(host)
	if strings.HasSuffix(h, "amazon.com") || strings.Contains(h, "amazon.") {
		return store.Careful
	}
	return store.Normal
}

// attempt fetches and extracts one entry.
func (a *application) attempt(ctx context.Context, entry store.QueueEntry) (store.Outcome, store.Observation) {
	obs := store.Observation{
		ItemID: entry.ItemID, ObservedAt: a.now(), Status: "no_data", Source: "jsonld",
	}

	client := fetch.New()
	if a.httpClient != nil {
		client.HTTP = a.httpClient
	}
	res := client.Get(ctx, entry.URL)

	if res.Err != nil {
		obs.Status = "error"
		obs.Detail = res.Err.Error()
		return store.OutcomeError, obs
	}
	if res.Blocked {
		obs.Status = "blocked"
		obs.Detail = res.BlockReason
		return store.OutcomeBlocked, obs
	}
	if !res.OK() {
		obs.Status = "error"
		obs.Detail = fmt.Sprintf("http %d", res.Status)
		return store.OutcomeError, obs
	}

	result, err := extract.JSONLD(res.Body)
	if err != nil {
		obs.Status = "error"
		obs.Detail = err.Error()
		return store.OutcomeError, obs
	}

	obs.Source = result.Source
	if result.Status == "priced" {
		obs.Status = "priced"
		obs.Price = result.Price
		obs.Currency = result.Currency
		obs.Available = result.Available
		return store.OutcomePriced, obs
	}

	obs.Status = "no_data"
	obs.Detail = result.Detail
	return store.OutcomeNoData, obs
}

// reportScrape prints the run summary and the per-host yield table, which is the
// honest measure of whether extraction works: overall coverage is dominated by
// Amazon's share of the item count and says little about the generic pass.
func (a *application) reportScrape(db *sql.DB, start time.Time, attempted, priced, noData, blocked, errored int) {
	fmt.Fprintf(a.stdout, "\n--- run summary (%.1fs) ---\n", time.Since(start).Seconds())
	fmt.Fprintf(a.stdout, "attempted %d: %d priced, %d no data, %d blocked, %d errors\n",
		attempted, priced, noData, blocked, errored)

	progress, err := store.Summarise(db, store.RefreshAfter, a.now())
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: summarising: %v\n", err)
		return
	}
	fmt.Fprintf(a.stdout, "queue: %d total, %d fresh, %d done, %d failed, %d blocked, %d stale, %d halted hosts\n",
		progress.Total, progress.Fresh, progress.Done, progress.Failed, progress.Blocked, progress.Stale, progress.Halted)

	reports, err := store.HostReports(db)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: host report: %v\n", err)
		return
	}
	if len(reports) == 0 {
		return
	}

	fmt.Fprintf(a.stdout, "\n--- per-host yield ---\n")
	fmt.Fprintf(a.stdout, "%-32s %5s %6s %7s %6s  %s\n", "host", "items", "priced", "nodata", "yield", "state")
	for _, r := range reports {
		state := ""
		if r.Halted {
			state = "HALTED"
		}
		yield := 0.0
		if r.Items > 0 {
			yield = 100 * float64(r.Priced) / float64(r.Items)
		}
		fmt.Fprintf(a.stdout, "%-32s %5d %6d %7d %5.0f%%  %s\n",
			trunc(r.Host, 32), r.Items, r.Priced, r.NoData, yield, state)
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func durationOrDefault(v string, def time.Duration) time.Duration {
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
