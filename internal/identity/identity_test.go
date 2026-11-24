package identity

import "testing"

func TestItemID(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
	}{
		{
			name:   "example with trailing slash",
			rawURL: "https://example.com/",
			want:   "0f115db062b7",
		},
		{
			name:   "example without trailing slash differs",
			rawURL: "https://example.com",
			want:   "100680ad546c",
		},
		{
			name:   "host case is not normalized",
			rawURL: "https://Example.com/",
			want:   "fa7436d27d32",
		},
		{
			name:   "fragment participates",
			rawURL: "https://www.sevensisterspdx.com/#sardine",
			want:   "ee46315f05c3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ItemID(tt.rawURL); got != tt.want {
				t.Fatalf("ItemID(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestStoreKey(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   string
		wantOK bool
	}{
		{
			name:   "www stripped",
			rawURL: "https://www.sevensisterspdx.com/",
			want:   "sevensisterspdx.com",
			wantOK: true,
		},
		{
			name:   "host lowercased",
			rawURL: "https://Sevensisterspdx.com/shop",
			want:   "sevensisterspdx.com",
			wantOK: true,
		},
		{
			name:   "path ignored",
			rawURL: "https://bookshop.org/books/foo",
			want:   "bookshop.org",
			wantOK: true,
		},
		{
			name:   "port stripped",
			rawURL: "http://localhost:8080/x",
			want:   "localhost",
			wantOK: true,
		},
		{
			name:   "only one www stripped",
			rawURL: "https://www.www.example.com/",
			want:   "www.example.com",
			wantOK: true,
		},
		{
			name:   "fragment ignored",
			rawURL: "https://example.com/foo#bar",
			want:   "example.com",
			wantOK: true,
		},
		{
			name:   "malformed URL",
			rawURL: "http://[::1",
			want:   "unknown",
			wantOK: false,
		},
		{
			name:   "relative URL has no host",
			rawURL: "example.com/foo",
			want:   "unknown",
			wantOK: false,
		},
		{
			name:   "empty URL",
			rawURL: "",
			want:   "unknown",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := StoreKey(tt.rawURL)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("StoreKey(%q) = (%q, %v), want (%q, %v)", tt.rawURL, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
