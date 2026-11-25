package render

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

const (
	// DefaultCollapseThreshold is the minimum run length worth collapsing.
	// Zero disables collapsing entirely: every run renders expanded, which is
	// the shipped default. The mechanism is kept because a future import could
	// produce dense single-day runs where folding genuinely helps.
	DefaultCollapseThreshold = 0

	dayLayout   = "January 2, 2006"
	monthLayout = "January 2006"
)

// dayView is one day heading and its runs, newest day first.
type dayView struct {
	Label string
	Runs  []runView
}

// runView is one consecutive group of items from the same store within a day.
type runView struct {
	StoreLabel string
	StoreHref  string
	Count      int
	CountLabel string
	RangeLabel string
	Collapsed  bool
	Cards      []cardView
}

// linkView is a labelled link used by archive and store navigation.
type linkView struct {
	Label string
	Href  string
}

// storeLinkView is one row of the /stores/ index.
type storeLinkView struct {
	Label      string
	Href       string
	CountLabel string
}

type archiveMonth struct {
	Year  int
	Month time.Month
	Items []model.Item
}

type archiveYear struct {
	Year   int
	Months []archiveMonth
}

// buildDays orders items newest first, groups them by UTC calendar day, and
// groups same-store runs within each day.
func buildDays(items []model.Item, storePaths map[string]string, base string, collapseAt int) []dayView {
	ordered := chronological(items)
	days := make([]dayView, 0)
	for start := 0; start < len(ordered); {
		day := ordered[start].AddedAt.UTC().Format("2006-01-02")
		end := start
		for end < len(ordered) && ordered[end].AddedAt.UTC().Format("2006-01-02") == day {
			end++
		}
		days = append(days, dayView{
			Label: ordered[start].AddedAt.UTC().Format(dayLayout),
			Runs:  buildRuns(ordered[start:end], storePaths, base, collapseAt),
		})
		start = end
	}
	return days
}

// buildRuns groups consecutive items sharing a store key. The caller has
// already restricted the items to one day. collapseAt of zero renders every run
// expanded.
func buildRuns(items []model.Item, storePaths map[string]string, base string, collapseAt int) []runView {
	runs := make([]runView, 0)
	for start := 0; start < len(items); {
		end := start + 1
		for end < len(items) && items[end].StoreKey == items[start].StoreKey {
			end++
		}
		run := items[start:end]

		label := run[0].StoreLabel
		if label == "" {
			label = run[0].StoreKey
		}
		runs = append(runs, runView{
			StoreLabel: label,
			StoreHref:  joinBase(base, storePathFor(run[0], storePaths)),
			Count:      len(run),
			CountLabel: countLabel(len(run)),
			RangeLabel: dateRangeLabel(run[len(run)-1].AddedAt, run[0].AddedAt),
			Collapsed:  collapseAt > 0 && len(run) >= collapseAt,
			Cards:      cardsFor(run, storePaths, base),
		})
		start = end
	}
	return runs
}

// chronological returns a copy of items ordered by added_at descending, with
// item ID ascending as the deterministic tie-break.
func chronological(items []model.Item) []model.Item {
	ordered := append([]model.Item(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].AddedAt.Equal(ordered[j].AddedAt) {
			return ordered[i].AddedAt.After(ordered[j].AddedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}

func cardsFor(items []model.Item, storePaths map[string]string, base string) []cardView {
	cards := make([]cardView, 0, len(items))
	for _, item := range items {
		cards = append(cards, cardFor(item, storePaths, base))
	}
	return cards
}

func countLabel(count int) string {
	if count == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", count)
}

// dateRangeLabel describes the oldest and newest added_at in a run. Runs stay
// inside one day, so the common result is a single date.
func dateRangeLabel(oldest, newest time.Time) string {
	o, n := oldest.UTC(), newest.UTC()
	if o.Format("2006-01-02") == n.Format("2006-01-02") {
		return n.Format(dayLayout)
	}
	return o.Format(dayLayout) + " – " + n.Format(dayLayout)
}

// buildArchives groups items by UTC year and month, newest first.
func buildArchives(items []model.Item) []archiveYear {
	byYear := make(map[int]map[time.Month][]model.Item)
	for _, item := range items {
		t := item.AddedAt.UTC()
		months, ok := byYear[t.Year()]
		if !ok {
			months = make(map[time.Month][]model.Item)
			byYear[t.Year()] = months
		}
		months[t.Month()] = append(months[t.Month()], item)
	}

	years := make([]archiveYear, 0, len(byYear))
	for year, months := range byYear {
		entry := archiveYear{Year: year}
		for month, monthItems := range months {
			entry.Months = append(entry.Months, archiveMonth{
				Year:  year,
				Month: month,
				Items: monthItems,
			})
		}
		sort.Slice(entry.Months, func(i, j int) bool {
			return entry.Months[i].Month > entry.Months[j].Month
		})
		years = append(years, entry)
	}
	sort.Slice(years, func(i, j int) bool {
		return years[i].Year > years[j].Year
	})
	return years
}

func monthLabel(year int, month time.Month) string {
	return time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Format(monthLayout)
}

func archiveYearPath(year int) string {
	return fmt.Sprintf("/archive/%04d/", year)
}

func archiveMonthPath(year int, month time.Month) string {
	return fmt.Sprintf("/archive/%04d/%02d/", year, int(month))
}

// buildStoreLinks orders the /stores/ index by item count descending, then
// label, then key.
func buildStoreLinks(stores []model.Store, base string) []storeLinkView {
	sorted := append([]model.Store(nil), stores...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, cj := len(sorted[i].ItemIDs), len(sorted[j].ItemIDs)
		if ci != cj {
			return ci > cj
		}
		li, lj := strings.ToLower(sorted[i].Label), strings.ToLower(sorted[j].Label)
		if li != lj {
			return li < lj
		}
		if sorted[i].Label != sorted[j].Label {
			return sorted[i].Label < sorted[j].Label
		}
		return sorted[i].Key < sorted[j].Key
	})

	links := make([]storeLinkView, 0, len(sorted))
	for _, store := range sorted {
		label := store.Label
		if label == "" {
			label = store.Key
		}
		links = append(links, storeLinkView{
			Label:      label,
			Href:       joinBase(base, store.Path),
			CountLabel: countLabel(len(store.ItemIDs)),
		})
	}
	return links
}

func storePathFor(item model.Item, storePaths map[string]string) string {
	if path, ok := storePaths[item.StoreKey]; ok {
		return path
	}
	return "/store/" + escapeSegment(item.StoreKey) + "/"
}
