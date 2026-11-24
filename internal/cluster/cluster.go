// Package cluster groups items into store collections and facets, and applies
// the single build ordering used everywhere.
package cluster

import (
	"net/url"
	"sort"
	"strings"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

// Apply derives facets and store collections, sorts items into build order,
// and returns the completed dataset.
func Apply(items []model.Item, rootTag string) model.Dataset {
	for i := range items {
		items[i].Facets = FacetNames(items[i].Tags, rootTag)
	}

	sortItems(items)

	storeGroups := make(map[string]*storeGroup)
	storeOrder := make([]string, 0)
	for i := range items {
		key := items[i].StoreKey
		group, ok := storeGroups[key]
		if !ok {
			group = &storeGroup{label: key}
			storeGroups[key] = group
			storeOrder = append(storeOrder, key)
		}
		if !group.labelSet && items[i].Overrides.Store != nil {
			if label := strings.TrimSpace(*items[i].Overrides.Store); label != "" {
				group.label = label
				group.labelSet = true
			}
		}
		group.indices = append(group.indices, i)
	}

	stores := make([]model.Store, 0, len(storeGroups))
	for _, key := range storeOrder {
		group := storeGroups[key]
		store := model.Store{
			Key:     key,
			Label:   group.label,
			Path:    "/store/" + escapeSegment(key) + "/",
			ItemIDs: make([]string, 0, len(group.indices)),
		}
		for _, index := range group.indices {
			items[index].StoreLabel = group.label
			store.ItemIDs = append(store.ItemIDs, items[index].ID)
		}
		stores = append(stores, store)
	}
	sort.Slice(stores, func(i, j int) bool {
		li, lj := strings.ToLower(stores[i].Label), strings.ToLower(stores[j].Label)
		if li != lj {
			return li < lj
		}
		if stores[i].Label != stores[j].Label {
			return stores[i].Label < stores[j].Label
		}
		return stores[i].Key < stores[j].Key
	})

	facetGroups := make(map[string][]int)
	for i := range items {
		for _, name := range items[i].Facets {
			facetGroups[name] = append(facetGroups[name], i)
		}
	}
	facetNames := make([]string, 0, len(facetGroups))
	for name := range facetGroups {
		facetNames = append(facetNames, name)
	}
	sort.Slice(facetNames, func(i, j int) bool {
		li, lj := strings.ToLower(facetNames[i]), strings.ToLower(facetNames[j])
		if li != lj {
			return li < lj
		}
		return facetNames[i] < facetNames[j]
	})

	facets := make([]model.Facet, 0, len(facetNames))
	for _, name := range facetNames {
		facet := model.Facet{
			Name:    name,
			Path:    "/facet/" + escapeSegment(name) + "/",
			ItemIDs: make([]string, 0, len(facetGroups[name])),
		}
		for _, index := range facetGroups[name] {
			facet.ItemIDs = append(facet.ItemIDs, items[index].ID)
		}
		facets = append(facets, facet)
	}

	return model.Dataset{
		Items:  items,
		Stores: stores,
		Facets: facets,
	}
}

// FacetNames returns the distinct facet names carried by tags. The root tag
// itself is not a facet, and nested values use their first segment:
// wishlist:book:fiction yields "book".
func FacetNames(tags []string, rootTag string) []string {
	prefix := strings.ToLower(rootTag) + ":"
	seen := make(map[string]bool)
	names := make([]string, 0)

	for _, tag := range tags {
		if !strings.HasPrefix(strings.ToLower(tag), prefix) {
			continue
		}
		rest := tag[len(rootTag)+1:]
		name := strings.SplitN(rest, ":", 2)[0]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		li, lj := strings.ToLower(names[i]), strings.ToLower(names[j])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
	return names
}

type storeGroup struct {
	label    string
	labelSet bool
	indices  []int
}

// escapeSegment makes a value safe to use as one URL path segment and one
// filesystem directory name. PathEscape handles slashes; the dot cases would
// otherwise allow a facet named ".." to escape its directory.
func escapeSegment(segment string) string {
	escaped := url.PathEscape(segment)
	switch escaped {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		return escaped
	}
}

func sortItems(items []model.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		ap, bp := a.Overrides.Priority, b.Overrides.Priority

		if ap != nil && bp != nil {
			if *ap != *bp {
				return *ap < *bp
			}
			return a.ID < b.ID
		}
		if ap != nil {
			return true
		}
		if bp != nil {
			return false
		}

		if !a.AddedAt.Equal(b.AddedAt) {
			return a.AddedAt.After(b.AddedAt)
		}
		return a.ID < b.ID
	})
}
