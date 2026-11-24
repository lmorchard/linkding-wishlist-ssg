package render

import (
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
