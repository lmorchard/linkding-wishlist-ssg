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
	"github.com/lmorchard/linkding-wishlist-ssg/internal/identity"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/lint"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/manifest"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/render"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/tier2"
)

type application struct {
	stdout        io.Writer
	stderr        io.Writer
	getenv        func(string) string
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

Flags:
  --out string             output directory (default "./build")
  --tag string             root selector tag (default "wishlist")
  --base-url string        prefix for absolute links (default "/")
  --include-archived       include archived bookmarks (debugging only)
  --lint-format string     lint report format: text or json (default "text")
  --no-lint                suppress the lint report

Environment:
  LINKDING_URL             Linkding base URL, no trailing slash
  LINKDING_TOKEN           Linkding API token
`)
}

func (a *application) runBuild(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: linkding-wishlist-ssg build [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}

	outDir := fs.String("out", "./build", "output directory; created if absent, wiped if present")
	tag := fs.String("tag", "wishlist", "root selector tag")
	baseURL := fs.String("base-url", "/", "prefix for absolute links")
	includeArchived := fs.Bool("include-archived", false, "include archived bookmarks (debugging only)")
	lintFormat := fs.String("lint-format", "text", "lint report format: text or json")
	noLint := fs.Bool("no-lint", false, "suppress the lint report")

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
	if *lintFormat != "text" && *lintFormat != "json" {
		fmt.Fprintf(a.stderr, "error: --lint-format must be \"text\" or \"json\", got %q\n", *lintFormat)
		return 1
	}
	if strings.TrimSpace(*tag) == "" {
		fmt.Fprintln(a.stderr, "error: --tag must not be empty")
		return 1
	}

	linkdingURL := strings.TrimSpace(a.getenv("LINKDING_URL"))
	if linkdingURL == "" {
		fmt.Fprintln(a.stderr, "error: LINKDING_URL is required (set it to your Linkding base URL)")
		return 1
	}
	linkdingToken := strings.TrimSpace(a.getenv("LINKDING_TOKEN"))
	if linkdingToken == "" {
		fmt.Fprintln(a.stderr, "error: LINKDING_TOKEN is required (set it to your Linkding API token)")
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

	fmt.Fprintf(a.stdout, "fetching bookmarks tagged %q...\n", *tag)
	bookmarks, err := client.ListAll(ctx, "#"+*tag, false, 100, a.progress)
	if err != nil {
		fmt.Fprintf(a.stderr, "error: fetching bookmarks: %v\n", err)
		return 1
	}
	if *includeArchived {
		archived, err := client.ListAll(ctx, "#"+*tag, true, 100, a.progress)
		if err != nil {
			fmt.Fprintf(a.stderr, "error: fetching archived bookmarks: %v\n", err)
			return 1
		}
		bookmarks = append(bookmarks, archived...)
	}

	if !*includeArchived {
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

	dataset := cluster.Apply(items, *tag)
	findings := lint.Run(dataset.Items)

	normalizedBase := normalizeBaseURL(*baseURL)
	files, err := render.Site(dataset, render.Options{BaseURL: normalizedBase, Tag: *tag})
	if err != nil {
		fmt.Fprintf(a.stderr, "error: rendering site: %v\n", err)
		return 1
	}

	var manifestBuf bytes.Buffer
	if err := manifest.Write(&manifestBuf, manifest.Build(dataset, manifest.Options{
		Tag:         *tag,
		BaseURL:     normalizedBase,
		GeneratedAt: a.now(),
	})); err != nil {
		fmt.Fprintf(a.stderr, "error: writing manifest: %v\n", err)
		return 1
	}
	files["manifest.json"] = manifestBuf.Bytes()

	if err := writeFiles(*outDir, files); err != nil {
		fmt.Fprintf(a.stderr, "error: writing output: %v\n", err)
		return 1
	}

	if !*noLint {
		if *lintFormat == "json" {
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
		len(dataset.Items), len(dataset.Stores), len(dataset.Facets), *outDir)
	return 0
}

func (a *application) progress(fetched, total int) {
	fmt.Fprintf(a.stdout, "  fetched %d/%d\n", fetched, total)
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
