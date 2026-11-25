package render

import (
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func taggedItem(id string, added time.Time, tags ...string) model.Item {
	return model.Item{
		ID:         id,
		URL:        "https://shop.example/" + id,
		StoreKey:   "shop.example",
		StoreLabel: "shop.example",
		AddedAt:    added,
		Tags:       tags,
	}
}

func TestSplitPinnedRemovesPinnedFromStream(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		taggedItem("a", base, "wishlist", "gaming"),
		taggedItem("b", base, "wishlist", "wishlist:featured"),
		taggedItem("c", base, "wishlist"),
	}

	pinned, stream := splitPinned(items, "wishlist:featured")

	if len(pinned) != 1 || pinned[0].ID != "b" {
		t.Fatalf("pinned = %v, want [b]", ids(pinned))
	}
	if len(stream) != 2 {
		t.Fatalf("stream = %v, want 2 items", ids(stream))
	}
	for _, item := range stream {
		if item.ID == "b" {
			t.Error("pinned item b is still present in stream")
		}
	}
}

func TestSplitPinnedEmptyTagKeepsEverythingInStream(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		taggedItem("a", base, "wishlist"),
		taggedItem("b", base, "wishlist:featured"),
	}

	pinned, stream := splitPinned(items, "")
	if len(pinned) != 0 {
		t.Errorf("pinned = %v, want none", ids(pinned))
	}
	if len(stream) != 2 {
		t.Errorf("stream = %v, want both items", ids(stream))
	}
}

func TestSplitPinnedMatchesExactTagOnly(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		// A prefix must not match: wishlist:featured-old is a different tag.
		taggedItem("a", base, "wishlist:featured-old"),
		taggedItem("b", base, "wishlist:featured"),
	}

	pinned, stream := splitPinned(items, "wishlist:featured")
	if len(pinned) != 1 || pinned[0].ID != "b" {
		t.Fatalf("pinned = %v, want [b]", ids(pinned))
	}
	if len(stream) != 1 || stream[0].ID != "a" {
		t.Fatalf("stream = %v, want [a]", ids(stream))
	}
}

func TestWithinWindowDropsOldItems(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	start := now.AddDate(0, -12, 0)

	items := []model.Item{
		taggedItem("old", start.Add(-24*time.Hour)),
		taggedItem("edge", start),
		taggedItem("recent", now),
	}

	got := withinWindow(items, start)
	if len(got) != 2 {
		t.Fatalf("kept %v, want [edge recent]", ids(got))
	}
	if got[0].ID != "edge" {
		t.Errorf("got[0] = %q, want edge (boundary is inclusive)", got[0].ID)
	}
}

func TestWithinWindowZeroStartKeepsAll(t *testing.T) {
	items := []model.Item{
		taggedItem("old", time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)),
		taggedItem("new", time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)),
	}
	if got := withinWindow(items, time.Time{}); len(got) != 2 {
		t.Errorf("kept %v, want all items when start is zero", ids(got))
	}
}

func ids(items []model.Item) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}
