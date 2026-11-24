// Package manifest emits the JSON manifest that is the public, machine-readable
// seam for future stateful layers. It carries no lint data.
package manifest

import (
	"encoding/json"
	"io"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

// SchemaVersion is the manifest schema emitted by this build.
const SchemaVersion = 1

// Manifest is the published JSON document.
type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	GeneratedAt   string  `json:"generated_at"`
	Source        Source  `json:"source"`
	Items         []Item  `json:"items"`
	Stores        []Store `json:"stores"`
	Facets        []Facet `json:"facets"`
}

// Source identifies what was queried. It never contains credentials.
type Source struct {
	Tag     string `json:"tag"`
	BaseURL string `json:"base_url"`
}

// Item is one manifest item.
type Item struct {
	ID          string           `json:"id"`
	URL         string           `json:"url"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Notes       string           `json:"notes"`
	Tags        []string         `json:"tags"`
	Facets      []string         `json:"facets"`
	StoreKey    string           `json:"store_key"`
	StoreLabel  string           `json:"store_label"`
	AddedAt     string           `json:"added_at"`
	ModifiedAt  string           `json:"modified_at"`
	Archived    bool             `json:"archived"`
	Overrides   *model.Overrides `json:"overrides,omitempty"`
}

// Store is one manifest store collection.
type Store struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Path    string   `json:"path"`
	ItemIDs []string `json:"item_ids"`
}

// Facet is one manifest facet collection.
type Facet struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	ItemIDs []string `json:"item_ids"`
}

// Options configures manifest construction.
type Options struct {
	Tag         string
	BaseURL     string
	GeneratedAt time.Time
}

// Build converts a dataset into a manifest.
func Build(dataset model.Dataset, opts Options) Manifest {
	items := make([]Item, 0, len(dataset.Items))
	for _, item := range dataset.Items {
		var overrides *model.Overrides
		if !item.Overrides.Empty() {
			copied := item.Overrides
			overrides = &copied
		}
		items = append(items, Item{
			ID:          item.ID,
			URL:         item.URL,
			Title:       item.Title,
			Description: item.Description,
			Notes:       item.Notes,
			Tags:        copyStrings(item.Tags),
			Facets:      copyStrings(item.Facets),
			StoreKey:    item.StoreKey,
			StoreLabel:  item.StoreLabel,
			AddedAt:     formatTime(item.AddedAt),
			ModifiedAt:  formatTime(item.ModifiedAt),
			Archived:    item.Archived,
			Overrides:   overrides,
		})
	}

	stores := make([]Store, 0, len(dataset.Stores))
	for _, store := range dataset.Stores {
		stores = append(stores, Store{
			Key:     store.Key,
			Label:   store.Label,
			Path:    store.Path,
			ItemIDs: copyStrings(store.ItemIDs),
		})
	}

	facets := make([]Facet, 0, len(dataset.Facets))
	for _, facet := range dataset.Facets {
		facets = append(facets, Facet{
			Name:    facet.Name,
			Path:    facet.Path,
			ItemIDs: copyStrings(facet.ItemIDs),
		})
	}

	return Manifest{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   opts.GeneratedAt.UTC().Format(time.RFC3339),
		Source: Source{
			Tag:     opts.Tag,
			BaseURL: opts.BaseURL,
		},
		Items:  items,
		Stores: stores,
		Facets: facets,
	}
}

// Write emits indented JSON with a trailing newline.
func Write(w io.Writer, m Manifest) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(m)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func copyStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}
