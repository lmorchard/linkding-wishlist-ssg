package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/config"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/extract"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/store"
)

type extractOptions struct {
	dbPath  string
	all     bool
	dryRun  bool
	timebox time.Duration
}

func (a *application) runExtract(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: linkding-wishlist-ssg extract [flags]

Reprocesses previously scraped raw payloads stored in SQLite without making any
network requests. Useful when extraction heuristics improve or new metadata
fields (thumbnails, descriptions, availability) are added.

Flags:
`)
		fs.PrintDefaults()
	}

	fs.String("store", "./wishlist.db", "path to the local observation store")
	fs.Bool("all", false, "reprocess all payloads, not just unpriced or missing-metadata items")
	fs.Bool("dry-run", false, "report what would be extracted without modifying the store")
	fs.Duration("timebox", 10*time.Minute, "maximum time to spend re-extracting")

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

	opts := extractOptions{
		dbPath:  resolve.String("store"),
		all:     resolve.Bool("all"),
		dryRun:  resolve.Bool("dry-run"),
		timebox: durationOrDefault(resolve.String("timebox"), 10*time.Minute),
	}

	return a.reprocess(ctx, opts)
}

func (a *application) reprocess(ctx context.Context, opts extractOptions) int {
	if strings.TrimSpace(opts.dbPath) == "" {
		fmt.Fprintln(a.stderr, "error: --store path must not be empty")
		return 1
	}
	if _, err := os.Stat(opts.dbPath); err != nil {
		fmt.Fprintf(a.stderr, "error: observation store %s: %v\n", opts.dbPath, err)
		return 1
	}

	db, err := store.Open(opts.dbPath)
	if err != nil {
		fmt.Fprintf(a.stderr, "error: opening %s: %v\n", opts.dbPath, err)
		return 1
	}
	defer db.Close()

	onlyMissing := !opts.all
	observations, err := store.RawPayloadObservations(db, onlyMissing)
	if err != nil {
		fmt.Fprintf(a.stderr, "error: querying raw payloads: %v\n", err)
		return 1
	}

	total := len(observations)
	if total == 0 {
		fmt.Fprintln(a.stdout, "no raw payloads found to reprocess")
		return 0
	}

	fmt.Fprintf(a.stdout, "reprocessing %d stored observation payload(s) from %s...\n", total, opts.dbPath)

	now := a.now()
	start := time.Now()
	deadline := start.Add(opts.timebox)

	var reprocessed, newlyPriced, newlyUnavailable, newImages, newDescriptions int

	for _, obs := range observations {
		if time.Now().After(deadline) {
			fmt.Fprintf(a.stdout, "\ntimebox reached after %s\n", opts.timebox)
			break
		}

		result, outcome, changed := a.reprocessOne(obs)
		if !changed {
			continue
		}

		reprocessed++
		if obs.Price == nil && result.Price != nil {
			newlyPriced++
		}
		if (obs.Available == nil || *obs.Available) && result.Availability.Unavailable() {
			newlyUnavailable++
		}
		if obs.ImageURL == "" && result.ImageURL != "" {
			newImages++
		}
		if obs.Description == "" && result.Description != "" {
			newDescriptions++
		}

		label := "-"
		if result.Price != nil {
			label = fmt.Sprintf("%.2f %s", *result.Price, result.Currency)
		}
		fmt.Fprintf(a.stdout, "%-8s %-12s %10s  %s\n", outcome, obs.ItemID, label, trunc(obs.URL, 50))

		if !opts.dryRun {
			updatedObs := obs
			if result.Price != nil {
				updatedObs.Price = result.Price
				updatedObs.Currency = result.Currency
			}
			if result.Available != nil {
				updatedObs.Available = result.Available
			}
			if result.ImageURL != "" {
				updatedObs.ImageURL = result.ImageURL
			}
			if result.Description != "" {
				updatedObs.Description = result.Description
			}
			if result.Status != "" {
				updatedObs.Status = result.Status
			}
			if result.Detail != "" {
				updatedObs.Detail = result.Detail
			}

			if err := store.UpdateObservation(db, updatedObs, outcome, now); err != nil {
				fmt.Fprintf(a.stderr, "error updating observation %s: %v\n", obs.ItemID, err)
			}
		}
	}

	fmt.Fprintf(a.stdout, "\n--- reprocess summary (%.1fs) ---\n", time.Since(start).Seconds())
	fmt.Fprintf(a.stdout, "inspected: %d, updated: %d\n", total, reprocessed)
	fmt.Fprintf(a.stdout, "newly priced: %d, newly unavailable: %d, new images: %d, new descriptions: %d\n",
		newlyPriced, newlyUnavailable, newImages, newDescriptions)

	return 0
}

// reprocessOne inspects an observation's raw payload and runs the appropriate
// extractor.
func (a *application) reprocessOne(obs store.Observation) (extract.Result, store.Outcome, bool) {
	if strings.TrimSpace(obs.RawPayload) == "" {
		return extract.Result{}, store.OutcomeNoData, false
	}

	var result extract.Result
	var err error

	// If source is jsonld or raw_payload contains ld+json, try JSONLD extractor
	if obs.Source == "jsonld" || strings.Contains(obs.RawPayload, "application/ld+json") {
		result, err = extract.JSONLD(obs.RawPayload)
	}

	if err != nil || result.Status == "no_data" {
		return extract.Result{}, store.OutcomeNoData, false
	}

	// Check if this extraction learned anything new over existing observation
	changed := false
	if obs.Price == nil && result.Price != nil {
		changed = true
	}
	if obs.ImageURL == "" && result.ImageURL != "" {
		changed = true
	}
	if obs.Description == "" && result.Description != "" {
		changed = true
	}
	if result.Availability.Unavailable() && obs.Status != "unavailable" {
		changed = true
	}

	outcome := store.OutcomePriced
	if result.Availability.Unavailable() {
		outcome = store.OutcomeUnavailable
	}

	return result, outcome, changed
}
