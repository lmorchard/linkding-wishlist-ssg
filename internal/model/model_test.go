package model

import (
	"testing"
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
