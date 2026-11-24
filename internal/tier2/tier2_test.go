package tier2

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func str(s string) *string   { return &s }
func num(n int) *int         { return &n }
func flt(f float64) *float64 { return &f }

func TestParse(t *testing.T) {
	tests := []struct {
		name          string
		notes         string
		want          model.Overrides
		wantWarnings  []string
		prefixWarning bool
	}{
		{
			name:  "no block",
			notes: "Just prose about a want.",
			want:  model.Overrides{},
		},
		{
			name:  "valid block with every key",
			notes: "Prose before.\n\n```wishlist\nstore: Seven Sisters\nprice: 42.00\ncurrency: USD\npriority: 1\nvariant: small, green\nimage: https://example.com/pic.jpg\n```\n\nProse after.",
			want: model.Overrides{
				Store:    str("Seven Sisters"),
				Price:    flt(42),
				Currency: str("USD"),
				Priority: num(1),
				Variant:  str("small, green"),
				Image:    str("https://example.com/pic.jpg"),
			},
			wantWarnings: []string{"tier2: image is not rendered in v0 (image key in https://example.com/x)"},
		},
		{
			name:  "info string is case-insensitive",
			notes: "```WISHLIST\nstore: Upper\n```",
			want:  model.Overrides{Store: str("Upper")},
		},
		{
			name:  "tilde fence accepted",
			notes: "~~~wishlist\nstore: Tilde\n~~~",
			want:  model.Overrides{Store: str("Tilde")},
		},
		{
			name:  "first block wins",
			notes: "```wishlist\nstore: First\n```\n\n```wishlist\nstore: Second\n```",
			want:  model.Overrides{Store: str("First")},
		},
		{
			name:  "empty block is a no-op",
			notes: "```wishlist\n```",
			want:  model.Overrides{},
		},
		{
			name:  "whitespace-only block is a no-op",
			notes: "```wishlist\n  \n\n```",
			want:  model.Overrides{},
		},
		{
			name:  "malformed YAML ignores the block",
			notes: "```wishlist\nstore: [unclosed\n```",
			want:  model.Overrides{},
			wantWarnings: []string{
				"tier2: parse error in https://example.com/x: yaml:",
			},
			prefixWarning: true,
		},
		{
			name:  "non-map YAML ignores the block",
			notes: "```wishlist\n- one\n- two\n```",
			want:  model.Overrides{},
			wantWarnings: []string{
				"tier2: parse error in https://example.com/x: yaml:",
			},
			prefixWarning: true,
		},
		{
			name:  "unterminated block warns and ignores",
			notes: "```wishlist\nstore: Never Closed",
			want:  model.Overrides{},
			wantWarnings: []string{
				"tier2: parse error in https://example.com/x: unterminated wishlist block",
			},
		},
		{
			name:  "unknown key warns and is ignored",
			notes: "```wishlist\nstore: Kept\nbogus: value\n```",
			want:  model.Overrides{Store: str("Kept")},
			wantWarnings: []string{
				`tier2: unknown key "bogus" in https://example.com/x`,
			},
		},
		{
			name:  "wrong types warn and are ignored",
			notes: "```wishlist\nstore: 5\npriority: high\nprice: cheap\ncurrency: 9\nvariant: true\nimage: false\n```",
			want:  model.Overrides{},
			wantWarnings: []string{
				`tier2: wrong type for "currency" in https://example.com/x`,
				`tier2: wrong type for "image" in https://example.com/x`,
				`tier2: wrong type for "price" in https://example.com/x`,
				`tier2: wrong type for "priority" in https://example.com/x`,
				`tier2: wrong type for "store" in https://example.com/x`,
				`tier2: wrong type for "variant" in https://example.com/x`,
			},
		},
		{
			name:  "integer price is accepted",
			notes: "```wishlist\nprice: 42\ncurrency: USD\n```",
			want:  model.Overrides{Price: flt(42), Currency: str("USD")},
		},
		{
			name:  "price without currency is dropped",
			notes: "```wishlist\nprice: 42.00\n```",
			want:  model.Overrides{},
			wantWarnings: []string{
				"tier2: price without currency in https://example.com/x",
			},
		},
		{
			name:  "empty currency counts as missing",
			notes: "```wishlist\nprice: 42.00\ncurrency: \"\"\n```",
			want:  model.Overrides{Currency: str("")},
			wantWarnings: []string{
				"tier2: price without currency in https://example.com/x",
			},
		},
		{
			name:  "currency without price is kept",
			notes: "```wishlist\ncurrency: USD\n```",
			want:  model.Overrides{Currency: str("USD")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser()
			got, warnings := parser.Parse("https://example.com/x", tt.notes)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("overrides = %#v, want %#v", got, tt.want)
			}
			if len(warnings) != len(tt.wantWarnings) {
				t.Fatalf("warnings = %#v, want %#v", warnings, tt.wantWarnings)
			}
			for i := range warnings {
				matched := warnings[i] == tt.wantWarnings[i]
				if tt.prefixWarning {
					matched = strings.HasPrefix(warnings[i], tt.wantWarnings[i])
				}
				if !matched {
					t.Fatalf("warning[%d] = %q, want %q", i, warnings[i], tt.wantWarnings[i])
				}
			}
		})
	}
}

func TestImageWarnsOnlyOncePerBuild(t *testing.T) {
	parser := NewParser()

	_, first := parser.Parse("https://one.example/x", "```wishlist\nimage: https://img/1.jpg\n```")
	_, second := parser.Parse("https://two.example/x", "```wishlist\nimage: https://img/2.jpg\n```")

	if len(first) != 1 || !strings.Contains(first[0], "not rendered") {
		t.Fatalf("first parse warnings = %#v, want one image warning", first)
	}
	if len(second) != 0 {
		t.Fatalf("second parse warnings = %#v, want none", second)
	}
}

func TestNonWishlistFenceIgnored(t *testing.T) {
	notes := "```yaml\nstore: Not An Override\n```\n\n```wishlist\nstore: Real\n```"
	got, warnings := NewParser().Parse("https://example.com/x", notes)
	if got.Store == nil || *got.Store != "Real" {
		t.Fatalf("store = %v, want Real", got.Store)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
}
