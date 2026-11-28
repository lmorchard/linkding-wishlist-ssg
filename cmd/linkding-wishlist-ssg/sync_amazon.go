package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"strings"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/config"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/fetch"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
)

const defaultAmazonListID = "1QWYI6P2JF3Q5"

func (a *application) runSyncAmazon(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("linkding-wishlist-ssg sync-amazon", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: linkding-wishlist-ssg sync-amazon [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}

	fs.String("list-id", defaultAmazonListID, "Amazon wishlist id")
	fs.String("cdp-url", "", "Chrome DevTools Protocol endpoint (e.g. http://127.0.0.1:9222)")
	fs.String("tag", "wishlist", "root selector tag for created bookmarks")
	fs.Bool("dry-run", false, "inspect new items without posting to Linkding")
	fs.Int("max-pages", 3, "maximum number of consecutive pages to inspect if all items are new")

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

	envFile := map[string]string{}
	if a.envFilePath != "" {
		if loaded, err := config.LoadEnv(a.envFilePath); err == nil {
			envFile = loaded
		}
	}
	resolve := siteconfig.NewResolver(fs, a.lookupEnv, envFile, func(msg string) {
		fmt.Fprintf(a.stderr, "warning: %s\n", msg)
	})

	listID := strings.TrimSpace(resolve.String("list-id"))
	cdpURL := strings.TrimSpace(resolve.String("cdp-url"))
	rootTag := strings.TrimSpace(resolve.String("tag"))
	dryRun := resolve.Bool("dry-run")
	maxPages := resolve.Int("max-pages")

	if listID == "" {
		fmt.Fprintln(a.stderr, "error: --list-id must not be empty")
		return 1
	}
	if cdpURL == "" {
		fmt.Fprintln(a.stderr, "error: --cdp-url is required for Amazon wishlist sync (e.g. http://127.0.0.1:9222)")
		return 1
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

	lClient, err := linkding.NewClient(linkding.ClientOptions{
		BaseURL:       linkdingURL,
		Token:         linkdingToken,
		HTTPClient:    a.httpClient,
		RetryBackoffs: a.retryBackoffs,
	})
	if err != nil {
		fmt.Fprintf(a.stderr, "error: %v\n", err)
		return 1
	}

	browser := fetch.NewBrowserClient(cdpURL)

	fmt.Fprintf(a.stdout, "sync-amazon: connecting to %s for wishlist %s...\n", cdpURL, listID)

	// Amazon wishlist sorted by date-added descending
	// unpurchased items first
	baseURL := fmt.Sprintf("https://www.amazon.com/hz/wishlist/ls/%s?filter=unpurchased&sort=date-added", url.PathEscape(listID))

	seenTotal := 0
	postedTotal := 0
	page := 1

	for page <= maxPages {
		pageURL := baseURL
		if page > 1 {
			pageURL = fmt.Sprintf("%s&page=%d", baseURL, page)
		}

		cards, err := browser.FetchAmazonWishlistPage(ctx, pageURL, 1)
		if err != nil {
			fmt.Fprintf(a.stderr, "error: fetching page %d: %v\n", page, err)
			return 1
		}

		if len(cards) == 0 {
			fmt.Fprintf(a.stdout, "page %d: 0 cards found, stopping.\n", page)
			break
		}

		newOnThisPage := 0
		alreadySeenOnThisPage := 0

		for _, card := range cards {
			if strings.TrimSpace(card.URL) == "" || card.URLSource == "unavailable" {
				continue
			}

			// Deduplicate against Linkding
			existing, err := lClient.CheckBookmark(ctx, card.URL)
			if err != nil {
				fmt.Fprintf(a.stderr, "warning: checking URL %s: %v\n", card.URL, err)
				continue
			}

			if existing != nil {
				alreadySeenOnThisPage++
				seenTotal++
				continue
			}

			// Unseen item!
			newOnThisPage++
			tags := []string{rootTag}
			if card.Shape == "amazon" {
				tags = append(tags, "amazon")
			} else {
				tags = append(tags, "imported:amazon")
			}

			title := card.Title
			if title == "" {
				title = card.URL
			}

			if dryRun {
				fmt.Fprintf(a.stdout, "[dry-run] would post: %s (%s) tags=%v\n", title, card.URL, tags)
			} else {
				created, err := lClient.CreateBookmark(ctx, linkding.CreateBookmarkRequest{
					URL:      card.URL,
					Title:    title,
					TagNames: tags,
				})
				if err != nil {
					fmt.Fprintf(a.stderr, "error creating bookmark for %s: %v\n", card.URL, err)
					continue
				}
				postedTotal++
				fmt.Fprintf(a.stdout, "created bookmark #%d: %s (%s)\n", created.ID, created.Title, created.URL)
			}
		}

		fmt.Fprintf(a.stdout, "page %d: %d card(s) inspected, %d new, %d already in Linkding\n",
			page, len(cards), newOnThisPage, alreadySeenOnThisPage)

		// Pagination rule: if ANY item was already seen, assume we've caught up with history!
		if alreadySeenOnThisPage > 0 {
			break
		}

		// If everything on this page was new, advance to next page
		page++
	}

	fmt.Fprintf(a.stdout, "sync-amazon finished: %d new item(s) posted, %d existing item(s) skipped\n",
		postedTotal, seenTotal)
	return 0
}
