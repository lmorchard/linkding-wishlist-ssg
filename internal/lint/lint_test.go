package lint

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func TestRulesFireAndDoNotFire(t *testing.T) {
	tests := []struct {
		name string
		item model.Item
		want []string
	}{
		{
			name: "facet absent does not fire",
			item: model.Item{ID: "a", URL: "https://example.com/a/b", Tags: []string{"wishlist"}},
			want: nil,
		},
		{
			name: "facet present does not fire",
			item: model.Item{ID: "a", URL: "https://example.com/a/b", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "root path is shallow",
			item: model.Item{ID: "a", URL: "https://example.com/", Facets: []string{"book"}},
			want: []string{CodeShallowPath},
		},
		{
			name: "empty path is shallow",
			item: model.Item{ID: "a", URL: "https://example.com", Facets: []string{"book"}},
			want: []string{CodeShallowPath},
		},
		{
			name: "single segment is not shallow",
			item: model.Item{ID: "a", URL: "https://example.com/shop", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "single segment with slash is not shallow",
			item: model.Item{ID: "a", URL: "https://example.com/shop/", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "two segments are not shallow",
			item: model.Item{ID: "a", URL: "https://example.com/shop/item", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "shallow path with identifying fragment does not fire",
			item: model.Item{ID: "a", URL: "https://gamersedition.com/#wishlist-unterzee-chart", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "shallow path with empty fragment still fires",
			item: model.Item{ID: "a", URL: "https://example.com/#", Facets: []string{"book"}},
			want: []string{CodeShallowPath},
		},
		{
			name: "deep path with fragment does not fire",
			item: model.Item{ID: "a", URL: "https://example.com/shop/item#anchor", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "title equal to store key fires",
			item: model.Item{ID: "a", URL: "https://www.example.com/a/b", Title: "example.com", Facets: []string{"book"}},
			want: []string{CodeTitleMatchesHost},
		},
		{
			name: "title equal to hostname with scheme and slash fires",
			item: model.Item{ID: "a", URL: "https://www.example.com/a/b", Title: "HTTPS://WWW.EXAMPLE.COM/", Facets: []string{"book"}},
			want: []string{CodeTitleMatchesHost},
		},
		{
			name: "ordinary title does not fire",
			item: model.Item{ID: "a", URL: "https://www.example.com/a/b", Title: "A Book", Facets: []string{"book"}},
			want: nil,
		},
		{
			name: "unparseable URL fires",
			item: model.Item{ID: "a", URL: "http://[::1", Title: "Broken", Facets: []string{"book"}},
			want: []string{CodeUnparseableURL},
		},
		{
			name: "multiple codes are ordered",
			item: model.Item{ID: "a", URL: "https://example.com/", Title: "example.com"},
			want: []string{CodeShallowPath, CodeTitleMatchesHost},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := Run([]model.Item{tt.item})
			if tt.want == nil {
				if len(findings) != 0 {
					t.Fatalf("findings = %#v, want none", findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("findings = %#v, want 1", findings)
			}
			if !reflect.DeepEqual(findings[0].Codes, tt.want) {
				t.Fatalf("codes = %v, want %v", findings[0].Codes, tt.want)
			}
			if len(findings[0].Suggestions) != len(tt.want) {
				t.Fatalf("suggestions = %v, want %d entries", findings[0].Suggestions, len(tt.want))
			}
		})
	}
}

func TestRunSkipsCleanItems(t *testing.T) {
	items := []model.Item{
		{ID: "clean", URL: "https://example.com/a/b", Title: "A Book", Facets: []string{"book"}},
		{ID: "flagged", URL: "https://example.com/", Title: "A Store", Facets: []string{"book"}},
	}
	findings := Run(items)
	if len(findings) != 1 || findings[0].ID != "flagged" {
		t.Fatalf("findings = %#v, want only flagged", findings)
	}
}

func TestWriteText(t *testing.T) {
	findings := []Finding{
		{
			ID:          "a1b2c3d4e5f6",
			URL:         "https://www.sevensisterspdx.com/",
			Codes:       []string{CodeShallowPath},
			Suggestions: []string{suggestionShallowPath},
		},
		{
			ID:          "b2c3d4e5f6a1",
			URL:         "https://example.com/",
			Codes:       []string{CodeTitleMatchesHost},
			Suggestions: []string{suggestionTitleMatchesHost},
		},
	}

	var buf bytes.Buffer
	if err := WriteText(&buf, findings); err != nil {
		t.Fatal(err)
	}

	want := "lint: 2 item(s) look off\n" +
		"  a1b2c3d4e5f6  https://www.sevensisterspdx.com/\n" +
		"    shallow_path        consider: is this a store, not a want?\n" +
		"  b2c3d4e5f6a1  https://example.com/\n" +
		"    title_matches_host  consider: is this a store, not a want?\n"
	if buf.String() != want {
		t.Fatalf("text report:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, Run(nil)); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Fatalf("empty report = %q, want []", buf.String())
	}

	buf.Reset()
	if err := WriteJSON(&buf, []Finding{{
		ID:          "a",
		URL:         "https://example.com/",
		Codes:       []string{CodeShallowPath},
		Suggestions: []string{suggestionShallowPath},
	}}); err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0]["id"] != "a" {
		t.Fatalf("decoded = %#v", decoded)
	}
}
