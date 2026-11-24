package cluster

import (
	"reflect"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func TestFacetNames(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{
			name: "root tag is not a facet",
			tags: []string{"wishlist"},
			want: []string{},
		},
		{
			name: "nested tag uses first segment",
			tags: []string{"wishlist", "wishlist:book:fiction"},
			want: []string{"book"},
		},
		{
			name: "deduplicates",
			tags: []string{"wishlist:book", "wishlist:book:fiction", "wishlist:book"},
			want: []string{"book"},
		},
		{
			name: "multiple facets sorted",
			tags: []string{"wishlist:kitchen", "wishlist:book"},
			want: []string{"book", "kitchen"},
		},
		{
			name: "unrelated tags ignored",
			tags: []string{"wishlist", "reading", "wishlistish:book"},
			want: []string{},
		},
		{
			name: "empty facet segment ignored",
			tags: []string{"wishlist:", "wishlist::book"},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FacetNames(tt.tags, "wishlist")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("FacetNames(%v) = %v, want %v", tt.tags, got, tt.want)
			}
		})
	}
}

func TestApplyOrdersItems(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	items := []model.Item{
		{ID: "f", StoreKey: "b.example", AddedAt: base.Add(3 * time.Hour)},
		{ID: "e", StoreKey: "a.example", AddedAt: base.Add(2 * time.Hour)},
		{ID: "d", StoreKey: "a.example", AddedAt: base.Add(1 * time.Hour), Overrides: model.Overrides{Priority: ptr(2)}},
		{ID: "c", StoreKey: "a.example", AddedAt: base.Add(4 * time.Hour), Overrides: model.Overrides{Priority: ptr(1)}},
		{ID: "b", StoreKey: "a.example", AddedAt: base.Add(1 * time.Hour)},
		{ID: "a", StoreKey: "a.example", AddedAt: base.Add(1 * time.Hour)},
	}

	got := Apply(items, "wishlist")
	ids := make([]string, len(got.Items))
	for i, item := range got.Items {
		ids[i] = item.ID
	}

	want := []string{"c", "d", "f", "e", "a", "b"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
}

func TestApplyGroupsStoresAndLabels(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	items := []model.Item{
		{ID: "one", StoreKey: "sevensisterspdx.com", AddedAt: base.Add(2 * time.Hour)},
		{ID: "two", StoreKey: "sevensisterspdx.com", AddedAt: base.Add(1 * time.Hour),
			Overrides: model.Overrides{Store: ptr("Seven Sisters")}},
		{ID: "three", StoreKey: "bookshop.org", AddedAt: base.Add(3 * time.Hour),
			Overrides: model.Overrides{Store: ptr("Bookshop")}},
	}

	got := Apply(items, "wishlist")

	if len(got.Stores) != 2 {
		t.Fatalf("stores = %#v, want 2", got.Stores)
	}
	// Stores sort by label: Bookshop before Seven Sisters.
	if got.Stores[0].Key != "bookshop.org" || got.Stores[0].Label != "Bookshop" {
		t.Fatalf("stores[0] = %#v", got.Stores[0])
	}
	if got.Stores[1].Key != "sevensisterspdx.com" || got.Stores[1].Label != "Seven Sisters" {
		t.Fatalf("stores[1] = %#v", got.Stores[1])
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(got.Stores[1].ItemIDs, want) {
		t.Fatalf("store item IDs = %v, want %v", got.Stores[1].ItemIDs, want)
	}
	if got.Stores[1].Path != "/store/sevensisterspdx.com/" {
		t.Fatalf("store path = %q", got.Stores[1].Path)
	}
	for _, item := range got.Items {
		if item.StoreKey == "sevensisterspdx.com" && item.StoreLabel != "Seven Sisters" {
			t.Fatalf("item %s label = %q, want Seven Sisters", item.ID, item.StoreLabel)
		}
	}
}

func TestApplyBuildsFacets(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	items := []model.Item{
		{ID: "one", StoreKey: "a.example", AddedAt: base.Add(2 * time.Hour), Tags: []string{"wishlist", "wishlist:book:fiction"}},
		{ID: "two", StoreKey: "b.example", AddedAt: base.Add(1 * time.Hour), Tags: []string{"wishlist", "wishlist:book"}},
	}

	got := Apply(items, "wishlist")
	if len(got.Facets) != 1 {
		t.Fatalf("facets = %#v, want 1", got.Facets)
	}
	facet := got.Facets[0]
	if facet.Name != "book" || facet.Path != "/facet/book/" {
		t.Fatalf("facet = %#v", facet)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(facet.ItemIDs, want) {
		t.Fatalf("facet item IDs = %v, want %v", facet.ItemIDs, want)
	}
}

func TestApplyEscapesUnsafePathSegments(t *testing.T) {
	items := []model.Item{
		{ID: "one", StoreKey: "..", Tags: []string{"wishlist:..", "wishlist:a/b"}},
	}
	got := Apply(items, "wishlist")

	if got.Stores[0].Path != "/store/%2E%2E/" {
		t.Fatalf("store path = %q, want /store/%%2E%%2E/", got.Stores[0].Path)
	}
	if len(got.Facets) != 2 {
		t.Fatalf("facets = %#v, want 2", got.Facets)
	}
	if got.Facets[0].Name != ".." || got.Facets[0].Path != "/facet/%2E%2E/" {
		t.Fatalf("facet = %#v", got.Facets[0])
	}
	if got.Facets[1].Name != "a/b" || got.Facets[1].Path != "/facet/a%2Fb/" {
		t.Fatalf("facet = %#v", got.Facets[1])
	}
}

func ptr[T any](v T) *T { return &v }
