// Package siteconfig holds the presentation knobs for a wishlist build.
//
// These are deliberately not Linkding concepts. Linkding has no opinion about
// what belongs on a front page, what counts as pinned, or which tags are too
// infrastructure-y to deserve a page. Each default here was chosen against real
// data (1,580 items, 405 flat tags) rather than from a naming convention nobody
// actually adopted.
package siteconfig

import "time"

// Defaults for every knob. Changing one of these changes the shipped site.
const (
	DefaultWindowMonths     = 12
	DefaultPinnedTag        = "wishlist:featured"
	DefaultTagPageThreshold = 3
)

// DefaultExcludedTags are selector and provenance tags, not topics. Rendering a
// page for them would reproduce the front page under a different heading.
//
// An entry ending in ":*" matches any tag with that prefix.
var DefaultExcludedTags = []string{
	"wishlist",   // the root selector; every item carries it
	"amazon",     // provenance: came from an Amazon product page
	"imported:*", // provenance: came from some import
}

// Config controls what the front page shows and which tag pages exist.
type Config struct {
	// WindowMonths is how far back the front page reaches. Zero or negative
	// means no window: show everything.
	//
	// Rolling, not calendar-aligned. A calendar year empties every January and
	// then refills, which reads as a bug.
	WindowMonths int

	// PinnedTag marks items that float to the top regardless of age. Pinned
	// items are rendered in their own day-grouped block and removed from the
	// stream below, so nothing appears twice on one page.
	PinnedTag string

	// TagPageThreshold is the minimum item count for a tag page to exist.
	// One- and two-item pages read worse than no page at all.
	TagPageThreshold int

	// CollapseThreshold is the minimum same-store run length worth folding
	// behind a <details> element. Zero — the default — renders every run
	// expanded, which keeps the page honest at the cost of some length.
	CollapseThreshold int

	// ExcludedTags are omitted from the tag index. Supports a ":*" suffix
	// meaning "any tag with this prefix".
	ExcludedTags []string
}

// Default returns the shipped configuration.
func Default() Config {
	return Config{
		WindowMonths:      DefaultWindowMonths,
		PinnedTag:         DefaultPinnedTag,
		TagPageThreshold:  DefaultTagPageThreshold,
		CollapseThreshold: 0,
		ExcludedTags:      append([]string(nil), DefaultExcludedTags...),
	}
}

// Excluded reports whether a tag should be omitted from the tag index.
func (c Config) Excluded(tag string) bool {
	for _, pattern := range c.ExcludedTags {
		if prefix, ok := trimStar(pattern); ok {
			if len(tag) >= len(prefix) && tag[:len(prefix)] == prefix {
				return true
			}
			continue
		}
		if tag == pattern {
			return true
		}
	}
	return false
}

func trimStar(pattern string) (prefix string, ok bool) {
	if len(pattern) > 1 && pattern[len(pattern)-2:] == ":*" {
		return pattern[:len(pattern)-1], true
	}
	return "", false
}

// WindowStart returns the earliest time the front page should include.
// The zero time means "no lower bound".
func (c Config) WindowStart(now time.Time) time.Time {
	if c.WindowMonths <= 0 {
		return time.Time{}
	}
	return now.AddDate(0, -c.WindowMonths, 0)
}
