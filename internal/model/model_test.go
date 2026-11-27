package model

import (
	"testing"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
)

func TestItemUserNotes(t *testing.T) {
	tests := []struct {
		name  string
		notes string
		want  string
	}{
		{
			name:  "empty",
			notes: "",
			want:  "",
		},
		{
			name:  "plain notes without block",
			notes: "I want this for Christmas in blue.",
			want:  "I want this for Christmas in blue.",
		},
		{
			name:  "notes before and after fenced block",
			notes: "First preference: size L.\n\n```wishlist\nstore: Acme\nprice: 19.99\n```\n\nOr whatever color is available.",
			want:  "First preference: size L.\n\nOr whatever color is available.",
		},
		{
			name:  "only fenced block",
			notes: "```wishlist\npriority: 1\n```",
			want:  "",
		},
		{
			name:  "tilde fence block",
			notes: "Remember to check coupon.\n~~~wishlist\nstore: Shop\n~~~\nThanks!",
			want:  "Remember to check coupon.\nThanks!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := Item{Notes: tt.notes}
			if got := it.UserNotes(); got != tt.want {
				t.Errorf("UserNotes() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPriorityTagExtraction(t *testing.T) {
	tests := []struct {
		name          string
		tags          []string
		notesPriority *int
		wantPriority  *int
	}{
		{
			name:         "p0 tag",
			tags:         []string{"wishlist", "wishlist:p0"},
			wantPriority: ptrInt(0),
		},
		{
			name:         "p1 tag case insensitive",
			tags:         []string{"wishlist", "Wishlist:P1"},
			wantPriority: ptrInt(1),
		},
		{
			name:         "p5 tag",
			tags:         []string{"wishlist", "wishlist:p5"},
			wantPriority: ptrInt(5),
		},
		{
			name:         "p6 tag ignored",
			tags:         []string{"wishlist", "wishlist:p6"},
			wantPriority: nil,
		},
		{
			name:         "no priority tag",
			tags:         []string{"wishlist", "books"},
			wantPriority: nil,
		},
		{
			name:          "tier 2 override wins over tag",
			tags:          []string{"wishlist", "wishlist:p2"},
			notesPriority: ptrInt(0),
			wantPriority:  ptrInt(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := linkding.Bookmark{
				URL:      "https://example.com/item",
				TagNames: tt.tags,
			}
			overrides := Overrides{
				Priority: tt.notesPriority,
			}
			it := FromBookmark(b, overrides)
			if tt.wantPriority == nil {
				if it.Overrides.Priority != nil {
					t.Errorf("expected Priority=nil, got %v", *it.Overrides.Priority)
				}
			} else {
				if it.Overrides.Priority == nil {
					t.Fatalf("expected Priority=%d, got nil", *tt.wantPriority)
				}
				if *it.Overrides.Priority != *tt.wantPriority {
					t.Errorf("Priority = %d, want %d", *it.Overrides.Priority, *tt.wantPriority)
				}
			}
		})
	}
}

func ptrInt(v int) *int {
	return &v
}
