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

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

//go:embed assets
var assetsFS embed.FS

// Options configures site rendering.
type Options struct {
	BaseURL string
	Tag     string
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
	view := pageView{
		Title:    "Wishlist",
		Heading:  "Wishlist",
		CSSHref:  joinBase(base, "/assets/style.css"),
		HomeHref: joinBase(base, "/"),
		Tag:      opts.Tag,
	}
	for _, facet := range dataset.Facets {
		view.Facets = append(view.Facets, facetLinkView{
			Name: facet.Name,
			Href: joinBase(base, facet.Path),
		})
	}
	for _, store := range dataset.Stores {
		section := sectionView{
			Label: store.Label,
			Href:  joinBase(base, store.Path),
		}
		for _, id := range store.ItemIDs {
			item, ok := itemsByID[id]
			if !ok {
				continue
			}
			section.Cards = append(section.Cards, cardFor(item, storePaths, base))
		}
		view.Sections = append(view.Sections, section)
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

	return files, nil
}

type pageView struct {
	Title    string
	Heading  string
	CSSHref  string
	HomeHref string
	Tag      string
	Sections []sectionView
	Facets   []facetLinkView
	Cards    []cardView
}

type sectionView struct {
	Label string
	Href  string
	Cards []cardView
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
	storePath, ok := storePaths[item.StoreKey]
	if !ok {
		storePath = "/store/" + escapeSegment(item.StoreKey) + "/"
	}

	card := cardView{
		Title:      item.DisplayTitle(),
		URL:        item.URL,
		StoreLabel: item.StoreLabel,
		StoreHref:  joinBase(base, storePath),
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
