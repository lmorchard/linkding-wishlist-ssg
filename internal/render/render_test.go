package render

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/cluster"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func testDataset() model.Dataset {
	added := time.Date(2026, 11, 1, 9, 14, 22, 0, time.UTC)
	priority := 1
	price := 42.0
	currency := "USD"
	variant := "small, green"
	store := "Seven Sisters"

	items := []model.Item{
		{
			ID:         "one",
			URL:        "https://www.sevensisterspdx.com/",
			Title:      "Seven Sisters",
			Tags:       []string{"wishlist"},
			StoreKey:   "sevensisterspdx.com",
			AddedAt:    added,
			ModifiedAt: added,
			Overrides: model.Overrides{
				Store:    &store,
				Priority: &priority,
				Price:    &price,
				Currency: &currency,
				Variant:  &variant,
			},
		},
		{
			ID:         "two",
			URL:        "https://bookshop.org/books/the-book",
			Title:      "",
			Tags:       []string{"wishlist", "wishlist:book:fiction"},
			StoreKey:   "bookshop.org",
			AddedAt:    added.Add(time.Hour),
			ModifiedAt: added.Add(time.Hour),
		},
		{
			ID:         "three",
			URL:        "https://bookshop.org/books/archived",
			Title:      "Archived Want",
			Tags:       []string{"wishlist", "wishlist:book"},
			StoreKey:   "bookshop.org",
			AddedAt:    added.Add(2 * time.Hour),
			ModifiedAt: added.Add(2 * time.Hour),
			Archived:   true,
		},
	}

	return cluster.Apply(items, "wishlist")
}

func TestSiteRendersPages(t *testing.T) {
	files, err := Site(testDataset(), Options{BaseURL: "/", Tag: "wishlist"})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"index.html",
		"store/sevensisterspdx.com/index.html",
		"store/bookshop.org/index.html",
		"facet/book/index.html",
		"assets/style.css",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("missing file %q; have %v", name, keys(files))
		}
	}

	index := string(files["index.html"])
	for _, want := range []string{
		"Seven Sisters",
		"the-book",
		"Archived Want",
		"USD 42.00",
		"small, green",
		"/store/sevensisterspdx.com/",
		"/facet/book/",
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}
	if strings.Contains(index, "USD 0") {
		t.Fatalf("index rendered a placeholder price:\n%s", index)
	}

	store := string(files["store/bookshop.org/index.html"])
	if !strings.Contains(store, "bookshop.org") || !strings.Contains(store, "the-book") {
		t.Fatalf("store page missing expected content:\n%s", store)
	}

	facet := string(files["facet/book/index.html"])
	if !strings.Contains(facet, "Facet: book") {
		t.Fatalf("facet page missing heading:\n%s", facet)
	}
	if !strings.Contains(facet, "Archived Want") {
		t.Fatalf("facet page missing archived item:\n%s", facet)
	}

	if !strings.Contains(index, "archived") {
		t.Fatalf("archived item has no marker:\n%s", index)
	}
}

func TestSiteAppliesBaseURL(t *testing.T) {
	files, err := Site(testDataset(), Options{BaseURL: "https://wishlist.example.com/", Tag: "wishlist"})
	if err != nil {
		t.Fatal(err)
	}
	index := string(files["index.html"])
	for _, want := range []string{
		`href="https://wishlist.example.com/assets/style.css"`,
		`href="https://wishlist.example.com/store/sevensisterspdx.com/"`,
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}
}

func TestCardOmitsMissingPrice(t *testing.T) {
	files, err := Site(testDataset(), Options{BaseURL: "/", Tag: "wishlist"})
	if err != nil {
		t.Fatal(err)
	}
	store := string(files["store/bookshop.org/index.html"])
	if strings.Contains(store, "card-price") {
		t.Fatalf("store page rendered a price element without price data:\n%s", store)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

func siteItem(id, storeKey, title string, added time.Time) model.Item {
	return model.Item{
		ID:         id,
		URL:        "https://" + storeKey + "/" + id,
		Title:      title,
		Tags:       []string{"wishlist"},
		StoreKey:   storeKey,
		AddedAt:    added,
		ModifiedAt: added,
	}
}

func renderSite(t *testing.T, items []model.Item) map[string][]byte {
	t.Helper()
	files, err := Site(cluster.Apply(items, "wishlist"), Options{BaseURL: "/", Tag: "wishlist"})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSiteCollapsesLongRuns(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		siteItem("a", "amazon.com", "First", base),
		siteItem("b", "amazon.com", "Second", base.Add(time.Minute)),
		siteItem("c", "amazon.com", "Third", base.Add(2*time.Minute)),
	}

	index := string(renderSite(t, items)["index.html"])
	for _, want := range []string{
		`<details class="run">`,
		"3 items",
		"November 24, 2026",
		"First",
		"Second",
		"Third",
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q:\n%s", want, index)
		}
	}
	if strings.Contains(index, `<details class="run" open>`) {
		t.Fatalf("long run rendered open:\n%s", index)
	}
}

func TestSiteKeepsShortRunsOpen(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		siteItem("a", "amazon.com", "First", base),
		siteItem("b", "amazon.com", "Second", base.Add(time.Minute)),
	}

	index := string(renderSite(t, items)["index.html"])
	if !strings.Contains(index, `<details class="run" open>`) {
		t.Fatalf("short run not rendered open:\n%s", index)
	}
	if !strings.Contains(index, "2 items") {
		t.Fatalf("short run count missing:\n%s", index)
	}
}

func TestSiteGroupsByDayNewestFirst(t *testing.T) {
	items := []model.Item{
		siteItem("a", "one.example", "Newest", time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)),
		siteItem("b", "one.example", "Older", time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)),
	}

	index := string(renderSite(t, items)["index.html"])
	newest := strings.Index(index, "November 24, 2026")
	older := strings.Index(index, "November 20, 2026")
	if newest < 0 || older < 0 {
		t.Fatalf("day headings missing:\n%s", index)
	}
	if newest > older {
		t.Fatalf("day headings out of order:\n%s", index)
	}
	if strings.Contains(index, "November 23, 2026") {
		t.Fatalf("empty day rendered:\n%s", index)
	}
}

func TestSiteRendersStoreIndex(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		siteItem("a", "small.example", "Small One", base),
		siteItem("b", "big.example", "Big One", base.Add(time.Minute)),
		siteItem("c", "big.example", "Big Two", base.Add(2*time.Minute)),
		siteItem("d", "big.example", "Big Three", base.Add(3*time.Minute)),
	}

	files := renderSite(t, items)
	page, ok := files["stores/index.html"]
	if !ok {
		t.Fatalf("missing stores/index.html; have %v", keys(files))
	}
	stores := string(page)
	big := strings.Index(stores, "big.example")
	small := strings.Index(stores, "small.example")
	if big < 0 || small < 0 {
		t.Fatalf("store links missing:\n%s", stores)
	}
	if big > small {
		t.Fatalf("stores not ordered by count:\n%s", stores)
	}
	if !strings.Contains(stores, "3 items") || !strings.Contains(stores, "1 item") {
		t.Fatalf("store counts missing:\n%s", stores)
	}
	if !strings.Contains(stores, `href="/store/big.example/"`) {
		t.Fatalf("store link missing:\n%s", stores)
	}
}

func TestSiteRendersArchivePages(t *testing.T) {
	items := []model.Item{
		siteItem("a", "one.example", "November Item", time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)),
		siteItem("b", "one.example", "September Item", time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
		siteItem("c", "one.example", "December Item", time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC)),
	}

	files := renderSite(t, items)
	for _, name := range []string{
		"archive/2026/index.html",
		"archive/2026/11/index.html",
		"archive/2026/09/index.html",
		"archive/2025/index.html",
		"archive/2025/12/index.html",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("missing %s; have %v", name, keys(files))
		}
	}

	november := string(files["archive/2026/11/index.html"])
	if !strings.Contains(november, "November Item") || strings.Contains(november, "September Item") {
		t.Fatalf("November archive has wrong items:\n%s", november)
	}
	if !strings.Contains(november, "November 24, 2026") {
		t.Fatalf("November archive is not day-grouped:\n%s", november)
	}

	year := string(files["archive/2026/index.html"])
	if !strings.Contains(year, "November Item") || !strings.Contains(year, "September Item") {
		t.Fatalf("year archive missing items:\n%s", year)
	}
	if !strings.Contains(year, `href="/archive/2026/11/"`) {
		t.Fatalf("year archive missing month link:\n%s", year)
	}

	index := string(files["index.html"])
	if !strings.Contains(index, `href="/archive/2026/"`) || !strings.Contains(index, `href="/archive/2025/"`) {
		t.Fatalf("index missing archive links:\n%s", index)
	}
}

func TestSiteEmitsNoJavaScript(t *testing.T) {
	items := []model.Item{
		siteItem("a", "one.example", "One", time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)),
		siteItem("b", "one.example", "Two", time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC)),
		siteItem("c", "one.example", "Three", time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)),
	}

	onAttribute := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	for name, body := range renderSite(t, items) {
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		page := string(body)
		if strings.Contains(page, "<script") {
			t.Fatalf("%s contains a script element", name)
		}
		if onAttribute.MatchString(page) {
			t.Fatalf("%s contains an inline event handler", name)
		}
	}
}
