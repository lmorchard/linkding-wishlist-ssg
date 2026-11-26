// Package model defines the item, store, and facet types the rest of the
// build operates on.
package model

import (
	"net/url"
	"strings"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/identity"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
)

// Overrides holds the recognized Tier 2 keys that were present in a
// bookmark's notes. Pointer fields distinguish "absent" from "zero".
type Overrides struct {
	Store    *string  `json:"store,omitempty"`
	Priority *int     `json:"priority,omitempty"`
	Price    *float64 `json:"price,omitempty"`
	Currency *string  `json:"currency,omitempty"`
	Variant  *string  `json:"variant,omitempty"`
	Image    *string  `json:"image,omitempty"`
}

// Empty reports whether no override key is present.
func (o Overrides) Empty() bool {
	return o.Store == nil && o.Priority == nil && o.Price == nil &&
		o.Currency == nil && o.Variant == nil && o.Image == nil
}

// Item is one wishlist want.
type Item struct {
	ID          string
	URL         string
	Title       string
	Description string
	Notes       string
	Tags        []string
	Facets      []string
	StoreKey    string
	StoreLabel  string
	AddedAt     time.Time
	ModifiedAt  time.Time
	Archived    bool
	Overrides   Overrides
	// Unavailable marks an item the shop says cannot be bought. It still
	// renders on store, tag, and archive pages — the want was real — but it is
	// held out of the front-page stream so the main view stays a live list.
	Unavailable bool
	ImageURL    string
}

// Store is a derived collection of items sharing a store key.
type Store struct {
	Key     string
	Label   string
	Path    string
	ItemIDs []string
}

// Facet is a derived collection of items sharing a wishlist:<name> tag.
type Facet struct {
	Name    string
	Path    string
	ItemIDs []string
}

// Dataset is the fully derived build input.
type Dataset struct {
	Items  []Item
	Stores []Store
	Facets []Facet
}

// FromBookmark maps a Linkding bookmark into an Item. Tier 2 overrides are
// supplied by the caller; store grouping is applied later by cluster.Apply.
func FromBookmark(b linkding.Bookmark, overrides Overrides) Item {
	storeKey, _ := identity.StoreKey(b.URL)

	added := b.DateAdded
	if added.IsZero() {
		added = b.DateCreated
	}
	modified := b.DateModified
	if modified.IsZero() {
		modified = added
	}

	tags := append([]string(nil), b.TagNames...)
	if tags == nil {
		tags = []string{}
	}

	return Item{
		ID:          identity.ItemID(b.URL),
		URL:         b.URL,
		Title:       b.Title,
		Description: b.Description,
		Notes:       b.Notes,
		Tags:        tags,
		StoreKey:    storeKey,
		AddedAt:     added,
		ModifiedAt:  modified,
		Archived:    b.IsArchived,
		Overrides:   overrides,
	}
}

// DisplayTitle returns the card title: the bookmark title when present,
// otherwise a readable form of the URL path, then the hostname, then the URL.
func (i Item) DisplayTitle() string {
	if strings.TrimSpace(i.Title) != "" {
		return i.Title
	}

	parsed, err := url.Parse(i.URL)
	if err == nil {
		if path := strings.Trim(parsed.Path, "/"); path != "" {
			return path
		}
		if parsed.Hostname() != "" {
			return parsed.Hostname()
		}
	}
	return i.URL
}

// HasPrice reports whether both price and currency are present and non-empty.
func (i Item) HasPrice() bool {
	return i.Overrides.Price != nil && i.Overrides.Currency != nil && *i.Overrides.Currency != ""
}
