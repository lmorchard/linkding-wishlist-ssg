// Package render turns a derived dataset into static HTML and CSS, embedded in
// the binary. No JavaScript is emitted.
package render

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
)

//go:embed assets
var assetsFS embed.FS

// Options configures site rendering.
type Options struct {
	BaseURL string
	Tag     string
	Config  siteconfig.Config
	Now     time.Time
}

// Site renders every page and returns files keyed by slash-separated relative
// path. Nothing touches disk until the caller decides the build succeeded.
func Site(dataset model.Dataset, opts Options) (map[string][]byte, error) {
	tmpl, err := template.New("site").ParseFS(assetsFS, "assets/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	css, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		return nil, fmt.Errorf("reading embedded CSS: %w", err)
	}

	itemsByID := make(map[string]model.Item, len(dataset.Items))
	storePaths := make(map[string]string, len(dataset.Stores))
	for _, store := range dataset.Stores {
		storePaths[store.Key] = store.Path
	}
	for _, item := range dataset.Items {
		itemsByID[item.ID] = item
	}

	files := make(map[string][]byte, 1+len(dataset.Stores)+len(dataset.Facets)+1)
	files["assets/style.css"] = css

	base := opts.BaseURL
	archives := buildArchives(dataset.Items)

	pinned, stream := splitPinned(dataset.Items, opts.Config.PinnedTag)
	// Unavailable items are held out of the front-page stream so the main view
	// stays a live list. They still appear on store, tag, and archive pages,
	// which are the complete record — bury, do not delete.
	stream = availableOnly(stream)
	stream = withinWindow(stream, opts.Config.WindowStart(opts.Now))

	view := pageView{
		Title:      "Wishlist",
		Heading:    "Wishlist",
		CSSHref:    joinBase(base, "/assets/style.css"),
		HomeHref:   joinBase(base, "/"),
		StoresHref: joinBase(base, "/stores/"),
		Tag:        opts.Tag,
		PinnedDays: buildDays(pinned, storePaths, base, opts.Config.CollapseThreshold),
		Days:       buildDays(stream, storePaths, base, opts.Config.CollapseThreshold),
		PinnedTag:  opts.Config.PinnedTag,
		TagsHref:   joinBase(base, "/tags/"),
	}
	for _, facet := range dataset.Facets {
		view.Facets = append(view.Facets, facetLinkView{
			Name: facet.Name,
			Href: joinBase(base, facet.Path),
		})
	}
	for _, year := range archives {
		view.Years = append(view.Years, linkView{
			Label: strconv.Itoa(year.Year),
			Href:  joinBase(base, archiveYearPath(year.Year)),
		})
	}

	indexHTML, err := execute(tmpl, "index", view)
	if err != nil {
		return nil, err
	}
	files["index.html"] = indexHTML

	for _, store := range dataset.Stores {
		storeView := pageView{
			Title:    store.Label + " — Wishlist",
			Heading:  store.Label,
			CSSHref:  joinBase(base, "/assets/style.css"),
			HomeHref: joinBase(base, "/"),
			Tag:      opts.Tag,
		}
		for _, id := range store.ItemIDs {
			item, ok := itemsByID[id]
			if !ok {
				continue
			}
			storeView.Cards = append(storeView.Cards, cardFor(item, storePaths, base))
		}
		page, err := execute(tmpl, "store", storeView)
		if err != nil {
			return nil, err
		}
		files[fileForPath(store.Path, "/store/"+escapeSegment(store.Key)+"/")] = page
	}

	for _, facet := range dataset.Facets {
		facetView := pageView{
			Title:    "Facet: " + facet.Name + " — Wishlist",
			Heading:  "Facet: " + facet.Name,
			CSSHref:  joinBase(base, "/assets/style.css"),
			HomeHref: joinBase(base, "/"),
			Tag:      opts.Tag,
		}
		for _, id := range facet.ItemIDs {
			item, ok := itemsByID[id]
			if !ok {
				continue
			}
			facetView.Cards = append(facetView.Cards, cardFor(item, storePaths, base))
		}
		page, err := execute(tmpl, "facet", facetView)
		if err != nil {
			return nil, err
		}
		files[fileForPath(facet.Path, "/facet/"+escapeSegment(facet.Name)+"/")] = page
	}

	storesView := pageView{
		Title:    "Stores — Wishlist",
		Heading:  "Stores",
		CSSHref:  joinBase(base, "/assets/style.css"),
		HomeHref: joinBase(base, "/"),
		Tag:      opts.Tag,
		Stores:   buildStoreLinks(dataset.Stores, base),
	}
	storesHTML, err := execute(tmpl, "stores", storesView)
	if err != nil {
		return nil, err
	}
	files["stores/index.html"] = storesHTML

	// Tag pages cover the full history, never the index window: a tag is a way
	// to browse everything matching it, so hiding old items would defeat it.
	tags := buildTags(dataset.Items, opts.Config)
	tagsIndexView := pageView{
		Title:      "Tags — Wishlist",
		Heading:    "Tags",
		CSSHref:    joinBase(base, "/assets/style.css"),
		HomeHref:   joinBase(base, "/"),
		StoresHref: joinBase(base, "/stores/"),
		Tag:        opts.Tag,
		TagsHref:   joinBase(base, "/tags/"),
	}
	for _, collection := range tags {
		tagsIndexView.Tags = append(tagsIndexView.Tags, tagLinkView{
			Name:       collection.Name,
			Href:       joinBase(base, collection.Path),
			Count:      len(collection.Items),
			CountLabel: countLabel(len(collection.Items)),
		})
	}
	tagsHTML, err := execute(tmpl, "tags", tagsIndexView)
	if err != nil {
		return nil, err
	}
	files["tags/index.html"] = tagsHTML

	for _, collection := range tags {
		tagView := pageView{
			Title:    collection.Name + " — Wishlist",
			Heading:  collection.Name,
			CSSHref:  joinBase(base, "/assets/style.css"),
			HomeHref: joinBase(base, "/"),
			Tag:      opts.Tag,
			TagsHref: joinBase(base, "/tags/"),
			Days:     buildDays(collection.Items, storePaths, base, opts.Config.CollapseThreshold),
		}
		page, err := execute(tmpl, "tag", tagView)
		if err != nil {
			return nil, err
		}
		files[fileForPath(collection.Path, collection.Path)] = page
	}

	for _, year := range archives {
		yearView := pageView{
			Title:    fmt.Sprintf("%d — Wishlist", year.Year),
			Heading:  strconv.Itoa(year.Year),
			CSSHref:  joinBase(base, "/assets/style.css"),
			HomeHref: joinBase(base, "/"),
			Tag:      opts.Tag,
		}
		yearItems := make([]model.Item, 0)
		for _, month := range year.Months {
			yearView.Months = append(yearView.Months, linkView{
				Label: monthLabel(month.Year, month.Month),
				Href:  joinBase(base, archiveMonthPath(month.Year, month.Month)),
			})
			yearItems = append(yearItems, month.Items...)
		}
		yearView.Days = buildDays(yearItems, storePaths, base, opts.Config.CollapseThreshold)

		yearPath := archiveYearPath(year.Year)
		yearHTML, err := execute(tmpl, "archive", yearView)
		if err != nil {
			return nil, err
		}
		files[fileForPath(yearPath, yearPath)] = yearHTML

		for _, month := range year.Months {
			monthView := pageView{
				Title:     monthLabel(month.Year, month.Month) + " — Wishlist",
				Heading:   monthLabel(month.Year, month.Month),
				CSSHref:   joinBase(base, "/assets/style.css"),
				HomeHref:  joinBase(base, "/"),
				Tag:       opts.Tag,
				YearHref:  joinBase(base, archiveYearPath(year.Year)),
				YearLabel: strconv.Itoa(year.Year),
				Days:      buildDays(month.Items, storePaths, base, opts.Config.CollapseThreshold),
			}
			monthPath := archiveMonthPath(month.Year, month.Month)
			monthHTML, err := execute(tmpl, "archive", monthView)
			if err != nil {
				return nil, err
			}
			files[fileForPath(monthPath, monthPath)] = monthHTML
		}
	}

	return files, nil
}

type pageView struct {
	Title    string
	Heading  string
	CSSHref  string
	HomeHref string
	Tag      string

	PinnedDays []dayView
	PinnedTag  string

	Facets []facetLinkView
	Cards  []cardView

	Days     []dayView
	Stores   []storeLinkView
	Years    []linkView
	Tags     []tagLinkView
	TagsHref string
	Months   []linkView

	StoresHref string
	YearHref   string
	YearLabel  string
}

type facetLinkView struct {
	Name string
	Href string
}

type cardView struct {
	Title      string
	URL        string
	StoreLabel string
	StoreHref  string
	Variant    string
	Price      string
	Archived   bool
}

func cardFor(item model.Item, storePaths map[string]string, base string) cardView {
	card := cardView{
		Title:      item.DisplayTitle(),
		URL:        item.URL,
		StoreLabel: item.StoreLabel,
		StoreHref:  joinBase(base, storePathFor(item, storePaths)),
		Archived:   item.Archived,
	}
	if card.StoreLabel == "" {
		card.StoreLabel = item.StoreKey
	}
	if item.Overrides.Variant != nil {
		card.Variant = *item.Overrides.Variant
	}
	if item.HasPrice() {
		card.Price = *item.Overrides.Currency + " " +
			strconv.FormatFloat(*item.Overrides.Price, 'f', 2, 64)
	}
	return card
}

// availableOnly drops items the shop reports as unavailable, for views that
// should show a live list. Pinned items are exempt: an editor deliberately
// featuring something outranks a stock flag.
func availableOnly(items []model.Item) []model.Item {
	out := make([]model.Item, 0, len(items))
	for _, item := range items {
		if item.Unavailable {
			continue
		}
		out = append(out, item)
	}
	return out
}

// splitPinned partitions items into pinned and stream. Pinned items are
// removed from the stream so nothing renders twice on one page.
func splitPinned(items []model.Item, pinnedTag string) (pinned, stream []model.Item) {
	if pinnedTag == "" {
		return nil, items
	}
	pinned = make([]model.Item, 0)
	stream = make([]model.Item, 0, len(items))
	for _, item := range items {
		if hasTag(item, pinnedTag) {
			pinned = append(pinned, item)
			continue
		}
		stream = append(stream, item)
	}
	return pinned, stream
}

func hasTag(item model.Item, tag string) bool {
	for _, t := range item.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// withinWindow drops items added before start. A zero start keeps everything.
func withinWindow(items []model.Item, start time.Time) []model.Item {
	if start.IsZero() {
		return items
	}
	kept := make([]model.Item, 0, len(items))
	for _, item := range items {
		if !item.AddedAt.Before(start) {
			kept = append(kept, item)
		}
	}
	return kept
}

func execute(tmpl *template.Template, name string, view pageView) ([]byte, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, view); err != nil {
		return nil, fmt.Errorf("rendering %s page: %w", name, err)
	}
	return buf.Bytes(), nil
}

func joinBase(base, path string) string {
	if base == "" {
		base = "/"
	}
	return strings.TrimRight(base, "/") + path
}

func fileForPath(pagePath, fallbackPath string) string {
	if pagePath == "" {
		pagePath = fallbackPath
	}
	return strings.TrimPrefix(pagePath, "/") + "index.html"
}

func escapeSegment(segment string) string {
	escaped := url.PathEscape(segment)
	switch escaped {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		return escaped
	}
}
