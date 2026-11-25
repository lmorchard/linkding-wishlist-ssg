package siteconfig

import (
	"testing"
	"time"
)

func TestExcluded(t *testing.T) {
	c := Default()

	cases := []struct {
		tag  string
		want bool
	}{
		{"wishlist", true},
		{"amazon", true},
		{"imported:amazon", true},
		{"imported:pinboard", true},
		{"clothing", false},
		{"gaming", false},
		// "imported" without the colon is a topic, not provenance.
		{"imported", false},
		{"amazonia", false},
	}

	for _, tc := range cases {
		if got := c.Excluded(tc.tag); got != tc.want {
			t.Errorf("Excluded(%q) = %v, want %v", tc.tag, got, tc.want)
		}
	}
}

func TestWindowStart(t *testing.T) {
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)

	if got, want := Default().WindowStart(now), time.Date(2025, 11, 25, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("default window start = %v, want %v", got, want)
	}

	noWindow := Config{WindowMonths: 0}
	if got := noWindow.WindowStart(now); !got.IsZero() {
		t.Errorf("zero-month window start = %v, want zero time", got)
	}

	custom := Config{WindowMonths: 3}
	if got, want := custom.WindowStart(now), time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("3-month window start = %v, want %v", got, want)
	}
}

func TestDefaultDoesNotShareExcludedSlice(t *testing.T) {
	a := Default()
	a.ExcludedTags[0] = "mutated"

	b := Default()
	if b.ExcludedTags[0] != DefaultExcludedTags[0] {
		t.Error("Default() shares its ExcludedTags backing array between calls")
	}
}
