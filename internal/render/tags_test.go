package render

import (
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
)

func tagsOf(collections []tagCollection) []string {
	out := make([]string, 0, len(collections))
	for _, c := range collections {
		out = append(out, c.Name)
	}
	return out
}

func TestBuildTagsExcludesInfrastructureTags(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		taggedItem("a", base, "wishlist", "amazon", "imported:amazon", "gaming"),
		taggedItem("b", base, "wishlist", "gaming"),
		taggedItem("c", base, "wishlist", "gaming"),
	}

	cfg := siteconfig.Default()
	cfg.TagPageThreshold = 1

	got := tagsOf(buildTags(items, cfg))
	for _, unwanted := range []string{"wishlist", "amazon", "imported:amazon"} {
		for _, name := range got {
			if name == unwanted {
				t.Errorf("tag %q should have been excluded, got %v", unwanted, got)
			}
		}
	}
	if len(got) != 1 || got[0] != "gaming" {
		t.Errorf("tags = %v, want [gaming]", got)
	}
}

func TestBuildTagsRespectsThreshold(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		taggedItem("a", base, "gaming", "clothing"),
		taggedItem("b", base, "gaming", "clothing"),
		taggedItem("c", base, "gaming", "rare"),
	}

	cfg := siteconfig.Default()
	cfg.TagPageThreshold = 3 // gaming qualifies
	got := tagsOf(buildTags(items, cfg))
	if len(got) != 1 || got[0] != "gaming" {
		t.Fatalf("threshold 3: tags = %v, want [gaming]", got)
	}

	cfg.TagPageThreshold = 2
	got = tagsOf(buildTags(items, cfg))
	if len(got) != 2 {
		t.Fatalf("threshold 2: tags = %v, want gaming and clothing", got)
	}

	cfg.TagPageThreshold = 0 // build everything
	got = tagsOf(buildTags(items, cfg))
	if len(got) != 3 {
		t.Fatalf("threshold 0: tags = %v, want all three", got)
	}
}

func TestBuildTagsSortedByCountThenName(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		taggedItem("a", base, "zebra", "apple"),
		taggedItem("b", base, "zebra", "apple"),
		taggedItem("c", base, "zebra"),
	}

	cfg := siteconfig.Default()
	cfg.TagPageThreshold = 1

	got := tagsOf(buildTags(items, cfg))
	// zebra has 3, apple has 2
	if len(got) != 2 || got[0] != "zebra" || got[1] != "apple" {
		t.Errorf("tags = %v, want [zebra apple]", got)
	}
}

func TestBuildTagsPathIsEscaped(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{taggedItem("a", base, "video games")}

	cfg := siteconfig.Default()
	cfg.TagPageThreshold = 1

	got := buildTags(items, cfg)
	if len(got) != 1 {
		t.Fatalf("got %d collections, want 1", len(got))
	}
	if got[0].Name != "video games" {
		t.Errorf("Name = %q, want the original tag", got[0].Name)
	}
	if got[0].Path == "/tag/video games/" {
		t.Error("Path was not escaped")
	}
}
