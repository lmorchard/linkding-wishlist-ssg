package render

import (
	"sort"
	"strings"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/siteconfig"
)

// tagCollection is one tag with its items, ready to render.
type tagCollection struct {
	Name  string
	Key   string // URL-safe path segment
	Path  string
	Items []model.Item
}

// buildTags groups items by tag, dropping infrastructure tags and tags below the
// threshold. Tags are the user's own flat vocabulary — no prefix convention is
// imposed, because the real data uses plain topical tags like "gaming" and
// "clothing" rather than "wishlist:gaming".
func buildTags(items []model.Item, cfg siteconfig.Config) []tagCollection {
	byTag := make(map[string][]model.Item)
	for _, item := range items {
		for _, tag := range item.Tags {
			if cfg.Excluded(tag) {
				continue
			}
			byTag[tag] = append(byTag[tag], item)
		}
	}

	collections := make([]tagCollection, 0, len(byTag))
	for name, tagged := range byTag {
		// threshold 0 means "build every non-excluded tag page"
		if cfg.TagPageThreshold > 0 && len(tagged) < cfg.TagPageThreshold {
			continue
		}
		collections = append(collections, tagCollection{
			Name:  name,
			Key:   escapeSegment(name),
			Path:  "/tag/" + escapeSegment(name) + "/",
			Items: tagged,
		})
	}

	// Most-populated first, then alphabetical. A tag index sorted only by name
	// buries the useful tags among one-off ones.
	sort.Slice(collections, func(i, j int) bool {
		if len(collections[i].Items) != len(collections[j].Items) {
			return len(collections[i].Items) > len(collections[j].Items)
		}
		return strings.ToLower(collections[i].Name) < strings.ToLower(collections[j].Name)
	})

	return collections
}

// tagLinkView is one row of the /tags/ index.
type tagLinkView struct {
	Name       string
	Href       string
	Count      int
	CountLabel string
}
