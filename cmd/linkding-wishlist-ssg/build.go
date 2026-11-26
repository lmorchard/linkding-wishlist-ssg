package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/cluster"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/config"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/identity"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/lint"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/manifest"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/render"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/store"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/tier2"
)

type application struct {
	stdout        io.Writer
	stderr        io.Writer
	lookupEnv     func(string) (string, bool)
	envFilePath   string
	now           func() time.Time
	httpClient    *http.Client
	retryBackoffs []time.Duration
}

func (a *application) run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.printUsage(a.stderr)
		return 1
	}

	switch args[0] {
	case "build":
		return a.runBuild(ctx, args[1:])
	case "scrape":
		return a.runScrape(ctx, args[1:])
	case "serve":
		return a.runServe(ctx, args[1:])
	case "extract":
		return a.runExtract(ctx, args[1:])
	case "help", "-h", "--help":
		a.printUsage(a.stdout)
		return 0
	default:
		fmt.Fprintf(a.stderr, "error: unknown command %q\n\n", args[0])
		a.printUsage(a.stderr)
		return 1
	}
}

func (a *application) printUsage(w io.Writer) {
	fmt.Fprint(w, `linkding-wishlist-ssg - render Linkding wishlist bookmarks as a static site

Usage:
  linkding-wishlist-ssg build [flags]
  linkding-wishlist-ssg scrape [flags]
  linkding-wishlist-ssg extract [flags]
  linkding-wishlist-ssg serve [flags]

Commands:
  build    render bookmarks as a static site (Linkding is read-only)
  scrape   fetch prices for queued items into a local observation store
  extract  reprocess previously scraped raw payloads in the local store
  serve    start a local HTTP static file server to preview the generated site

Scraping is a separate step from building. Run "scrape" from cron, then "build";
the two are independent so a scrape failure cannot stop the site from rendering.

Flags:
  --out string             output directory (default "./build")
  --tag string             root selector tag (default "wishlist")
  --base-url string        prefix for absolute links (default "/")
  --include-archived       include archived bookmarks (debugging only)
  --lint-format string     lint report format: text or json (default "text")
  --no-lint                suppress the lint report

Front page and tag pages:
  --index-months int           front page reaches back this many months; 0 shows everything (default 12)
  --pinned-tag string          tag whose items float to the top of the front page
                               (default "wishlist:featured")
  --tag-page-threshold int     minimum items for a tag page to exist; 0 builds every tag
                               (default 3)
  --exclude-tags string        comma-separated tags omitted from the tag index; a trailing
                               :* matches a prefix (default "wishlist,amazon,imported:*")
  --collapse-threshold int     fold same-store runs of at least this many items behind
                               <details>; 0 keeps every run expanded (default 0)

Server flags (for "serve"):
  --dir string                 directory of static files to serve (default "./build")
  --addr string                address and port to bind (default ":8080")

  Every flag can also be set by environment variable; see below.

Environment:
  LINKDING_URL             Linkding base URL, no trailing slash
  LINKDING_TOKEN           Linkding API token

  Every option above also reads an environment variable, prefixed
  LINKDING_WISHLIST_ and upper-cased with dashes as underscores:

    --dir                  ->  LINKDING_WISHLIST_DIR
    --addr                 ->  LINKDING_WISHLIST_ADDR
    --index-months         ->  LINKDING_WISHLIST_INDEX_MONTHS
    --pinned-tag           ->  LINKDING_WISHLIST_PINNED_TAG
    --tag-page-threshold   ->  LINKDING_WISHLIST_TAG_PAGE_THRESHOLD
    --exclude-tags         ->  LINKDING_WISHLIST_EXCLUDE_TAGS
    --collapse-threshold   ->  LINKDING_WISHLIST_COLLAPSE_THRESHOLD
    --out                  ->  LINKDING_WISHLIST_OUT
    --tag                  ->  LINKDING_WISHLIST_TAG
    --base-url             ->  LINKDING_WISHLIST_BASE_URL
    --include-archived     ->  LINKDING_WISHLIST_INCLUDE_ARCHIVED
    --lint-format          ->  LINKDING_WISHLIST_LINT_FORMAT
    --no-lint              ->  LINKDING_WISHLIST_NO_LINT

  Precedence: explicit flag, then process environment, then .env, then the
  flag's default. A variable set to an empty string still counts as set, so it
  can clear a .env value without editing the file.

  A .env file in the current directory is loaded if present.
`)
}

func (a *application) runBuild(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: linkding-wishlist-ssg build [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprint(fs.Output(), "\nCredentials come from LINKDING_URL and LINKDING_TOKEN, or from a .env\nfile in the current directory. The process environment wins.\n")
	}

	// Flags are declared here for their names, defaults, and help text. Values
	// are read later through the layered resolver, never from these handles,
	// because the winner may come from the environment or .env instead.
	fs.String("out", "./build", "output directory; created if absent, wiped if present")
	fs.String("tag", "wishlist", "root selector tag")
	fs.String("base-url", "/", "prefix for absolute links")
	fs.Bool("include-archived", false, "include archived bookmarks (debugging only)")
	fs.String("lint-format", "text", "lint report format: text or json")
	fs.Bool("no-lint", false, "suppress the lint report")
	fs.String("store", "./wishlist.db",
		"observation store to read scraped prices from; missing file is not an error")

	fs.Int("index-months", siteconfig.DefaultWindowMonths,
		"front page reaches back this many months; 0 shows everything")
	fs.String("pinned-tag", siteconfig.DefaultPinnedTag,
		"tag whose items float to the top of the front page")
	fs.Int("tag-page-threshold", siteconfig.DefaultTagPageThreshold,
		"minimum items for a tag page to exist; 0 builds pages for every tag")
	fs.String("exclude-tags", strings.Join(siteconfig.DefaultExcludedTags, ","),
		"comma-separated tags omitted from the tag index; trailing :* matches a prefix")
	fs.Int("collapse-threshold", 0,
		"fold same-store runs of at least this many items behind <details>; 0 keeps every run expanded")

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
		loaded, err := config.LoadEnv(a.envFilePath)
		if err != nil {
			fmt.Fprintf(a.stderr, "warning: reading %s: %v\n", a.envFilePath, err)
		} else {
			envFile = loaded
		}
	}

	// Every option resolves through the same precedence: explicit flag, then
	// process environment, then .env, then the flag's default. Descriptions and
	// defaults stay on the flag definitions; only the winning value is read here.
	resolve := siteconfig.NewResolver(fs, a.lookupEnv, envFile, func(msg string) {
		fmt.Fprintf(a.stderr, "warning: %s\n", msg)
	})

	outDir := resolve.String("out")
	rootTag := resolve.String("tag")
	baseURL := resolve.String("base-url")
	includeArchived := resolve.Bool("include-archived")
	lintFormat := resolve.String("lint-format")
	noLint := resolve.Bool("no-lint")
	storePath := resolve.String("store")

	windowMonths := resolve.Int("index-months")
	pinnedTag := resolve.String("pinned-tag")
	tagThreshold := resolve.Int("tag-page-threshold")
	excludedFlag := resolve.String("exclude-tags")
	collapseThreshold := resolve.Int("collapse-threshold")

	if lintFormat != "text" && lintFormat != "json" {
		fmt.Fprintf(a.stderr, "error: --lint-format must be \"text\" or \"json\", got %q\n", lintFormat)
		return 1
	}
	if strings.TrimSpace(rootTag) == "" {
		fmt.Fprintln(a.stderr, "error: --tag must not be empty")
		return 1
	}
	if windowMonths < 0 {
		fmt.Fprintln(a.stderr, "error: --index-months must not be negative")
		return 1
	}
	if tagThreshold < 0 {
		fmt.Fprintln(a.stderr, "error: --tag-page-threshold must not be negative")
		return 1
	}
	if collapseThreshold < 0 {
		fmt.Fprintln(a.stderr, "error: --collapse-threshold must not be negative")
		return 1
	}

	siteCfg := siteconfig.Config{
		WindowMonths:      windowMonths,
		PinnedTag:         strings.TrimSpace(pinnedTag),
		TagPageThreshold:  tagThreshold,
		CollapseThreshold: collapseThreshold,
		ExcludedTags:      splitTags(excludedFlag),
	}

	linkdingURL := strings.TrimSpace(a.envValue(envFile, "LINKDING_URL"))
	if linkdingURL == "" {
		fmt.Fprintln(a.stderr, "error: LINKDING_URL is required (set it in the environment or .env)")
		return 1
	}
	linkdingToken := strings.TrimSpace(a.envValue(envFile, "LINKDING_TOKEN"))
	if linkdingToken == "" {
		fmt.Fprintln(a.stderr, "error: LINKDING_TOKEN is required (set it in the environment or .env)")
		return 1
	}

	client, err := linkding.NewClient(linkding.ClientOptions{
		BaseURL:       linkdingURL,
		Token:         linkdingToken,
		HTTPClient:    a.httpClient,
		RetryBackoffs: a.retryBackoffs,
	})
	if err != nil {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.stdout, "fetching bookmarks tagged %q...\n", rootTag)
	bookmarks, err := client.ListAll(ctx, "#"+rootTag, false, 100, a.progress)
	if err != nil {
		fmt.Fprintf(a.stderr, "error: fetching bookmarks: %v\n", err)
		return 1
	}
	if includeArchived {
		archived, err := client.ListAll(ctx, "#"+rootTag, true, 100, a.progress)
		if err != nil {
			fmt.Fprintf(a.stderr, "error: fetching archived bookmarks: %v\n", err)
			return 1
		}
		bookmarks = append(bookmarks, archived...)
	}

	if !includeArchived {
		filtered := bookmarks[:0]
		for _, bookmark := range bookmarks {
			if !bookmark.IsArchived {
				filtered = append(filtered, bookmark)
			}
		}
		bookmarks = filtered
	}

	parser := tier2.NewParser()
	items := make([]model.Item, 0, len(bookmarks))
	seenIDs := make(map[string]string, len(bookmarks))
	for _, bookmark := range bookmarks {
		if strings.TrimSpace(bookmark.URL) == "" {
			fmt.Fprintf(a.stderr, "error: Linkding returned bookmark %d with an empty URL\n", bookmark.ID)
			return 1
		}

		id := identity.ItemID(bookmark.URL)
		if existingURL, ok := seenIDs[id]; ok {
			if existingURL != bookmark.URL {
				fmt.Fprintf(a.stderr, "error: item ID collision between distinct URLs:\n  %s\n  %s\n", existingURL, bookmark.URL)
				return 1
			}
			fmt.Fprintf(a.stderr, "warning: duplicate bookmark URL skipped: %s\n", bookmark.URL)
			continue
		}
		seenIDs[id] = bookmark.URL

		overrides, warnings := parser.Parse(bookmark.URL, bookmark.Notes)
		for _, warning := range warnings {
			fmt.Fprintf(a.stderr, "warning: %s\n", warning)
		}
		items = append(items, model.FromBookmark(bookmark, overrides))
	}

	// Observed prices fill in only where a human wrote no override. The
	// precedence is deliberate: notes are intent, scraping is guesswork, and
	// intent wins even when the guess is fresher.
	if priced, unavailable := a.applyObservedPrices(items, storePath); priced > 0 || unavailable > 0 {
		fmt.Fprintf(a.stdout, "applied %d observed price(s), %d unavailable item(s) from %s\n",
			priced, unavailable, storePath)
	}

	dataset := cluster.Apply(items, rootTag)
	findings := lint.Run(dataset.Items)

	normalizedBase := normalizeBaseURL(baseURL)
	files, err := render.Site(dataset, render.Options{
		BaseURL: normalizedBase,
		Tag:     rootTag,
		Config:  siteCfg,
		Now:     a.now(),
	})
	if err != nil {
		fmt.Fprintf(a.stderr, "error: rendering site: %v\n", err)
		return 1
	}

	var manifestBuf bytes.Buffer
	if err := manifest.Write(&manifestBuf, manifest.Build(dataset, manifest.Options{
		Tag:         rootTag,
		BaseURL:     normalizedBase,
		GeneratedAt: a.now(),
	})); err != nil {
		fmt.Fprintf(a.stderr, "error: writing manifest: %v\n", err)
		return 1
	}
	files["manifest.json"] = manifestBuf.Bytes()

	if err := writeFiles(outDir, files); err != nil {
		fmt.Fprintf(a.stderr, "error: writing output: %v\n", err)
		return 1
	}

	if !noLint {
		if lintFormat == "json" {
			err = lint.WriteJSON(a.stdout, findings)
		} else {
			err = lint.WriteText(a.stdout, findings)
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "error: writing lint report: %v\n", err)
			return 1
		}
	}

	fmt.Fprintf(a.stdout, "build: %d item(s), %d store(s), %d facet(s) -> %s\n",
		len(dataset.Items), len(dataset.Stores), len(dataset.Facets), outDir)
	return 0
}

func (a *application) progress(fetched, total int) {
	fmt.Fprintf(a.stdout, "  fetched %d/%d\n", fetched, total)
}

// envValue returns the process environment value when the variable is set,
// even to an empty string. Values from .env are only a fallback.
func (a *application) envValue(fileValues map[string]string, key string) string {
	if a.lookupEnv != nil {
		if value, ok := a.lookupEnv(key); ok {
			return value
		}
	}
	return fileValues[key]
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

func normalizeBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "/"
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base
}

// splitTags parses a comma-separated tag list, dropping blanks so a trailing
// comma or an empty flag does not produce a phantom exclusion.
func splitTags(raw string) []string {
	tags := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		if tag := strings.TrimSpace(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func writeFiles(outDir string, files map[string][]byte) error {
	if err := ensureSafeOutputDir(outDir); err != nil {
		return err
	}
	if err := os.RemoveAll(outDir); err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(outDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func ensureSafeOutputDir(outDir string) error {
	absolute, err := filepath.Abs(outDir)
	if err != nil {
		return fmt.Errorf("resolving output directory: %w", err)
	}
	if absolute == string(filepath.Separator) {
		return fmt.Errorf("refusing to use the filesystem root as --out")
	}
	if cwd, err := os.Getwd(); err == nil && absolute == cwd {
		return fmt.Errorf("refusing to wipe the current directory; pass a subdirectory to --out")
	}
	if home, err := os.UserHomeDir(); err == nil && absolute == home {
		return fmt.Errorf("refusing to wipe the home directory; pass a subdirectory to --out")
	}
	return nil
}

// applyObservedPrices copies scraped prices onto items that carry no Tier-2
// price override, and marks items the shop reports as unavailable. It returns
// how many items changed.
//
// A missing store is not an error: the site must build on a machine that has
// never scraped anything, and a fresh clone should render rather than complain.
func (a *application) applyObservedPrices(items []model.Item, storePath string) (priced, unavailable int) {
	if strings.TrimSpace(storePath) == "" {
		return 0, 0
	}
	if _, err := os.Stat(storePath); err != nil {
		return 0, 0
	}

	db, err := store.Open(storePath)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: opening %s: %v\n", storePath, err)
		return 0, 0
	}
	defer db.Close()

	prices, err := store.LatestPrices(db)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: reading prices from %s: %v\n", storePath, err)
		return 0, 0
	}
	states, err := store.UnavailableItems(db)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: reading availability from %s: %v\n", storePath, err)
		return 0, 0
	}

	for i := range items {
		if states[items[i].ID] {
			items[i].Unavailable = true
			unavailable++
		}

		obs, ok := prices[items[i].ID]
		if !ok {
			continue
		}

		// Overlay observed image if item has no Tier-2 image override
		if items[i].Overrides.Image == nil && obs.ImageURL != "" {
			items[i].ImageURL = obs.ImageURL
		}

		// Overlay observed description if Linkding description is empty
		if strings.TrimSpace(items[i].Description) == "" && obs.Description != "" {
			items[i].Description = obs.Description
		}

		if items[i].Overrides.Price != nil {
			continue
		}
		if obs.Price == nil || obs.Currency == "" {
			continue
		}
		price, currency := *obs.Price, obs.Currency
		items[i].Overrides.Price = &price
		items[i].Overrides.Currency = &currency
		priced++
	}
	return priced, unavailable
}
