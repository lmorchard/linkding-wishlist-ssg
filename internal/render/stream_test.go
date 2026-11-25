package render

import (
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func streamItem(id, storeKey string, added time.Time) model.Item {
	return model.Item{
		ID:         id,
		URL:        "https://" + storeKey + "/" + id,
		StoreKey:   storeKey,
		StoreLabel: storeKey,
		AddedAt:    added,
	}
}

func TestChronologicalOrdersNewestFirst(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		streamItem("a", "one.example", base),
		streamItem("b", "two.example", base.Add(2*time.Hour)),
		streamItem("c", "three.example", base),
	}

	got := chronological(items)
	want := []string{"b", "a", "c"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestBuildRunsCollapsesAtThreshold(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		keys          []string
		wantCount     []int
		wantCollapsed []bool
	}{
		{
			name:          "one item stays open",
			keys:          []string{"a"},
			wantCount:     []int{1},
			wantCollapsed: []bool{false},
		},
		{
			name:          "two items stay open",
			keys:          []string{"a", "a"},
			wantCount:     []int{2},
			wantCollapsed: []bool{false},
		},
		{
			name:          "three items collapse",
			keys:          []string{"a", "a", "a"},
			wantCount:     []int{3},
			wantCollapsed: []bool{true},
		},
		{
			name:          "interleaved stores are separate runs",
			keys:          []string{"a", "a", "b", "a", "a"},
			wantCount:     []int{2, 1, 2},
			wantCollapsed: []bool{false, false, false},
		},
		{
			name:          "long run then short runs",
			keys:          []string{"a", "a", "a", "a", "b", "a", "a"},
			wantCount:     []int{4, 1, 2},
			wantCollapsed: []bool{true, false, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := make([]model.Item, 0, len(tt.keys))
			for i, key := range tt.keys {
				items = append(items, streamItem(
					string(rune('a'+i)), key, base.Add(time.Duration(-i)*time.Minute)))
			}
			runs := buildRuns(items, nil, "/", 3)
			if len(runs) != len(tt.wantCount) {
				t.Fatalf("runs = %d, want %d", len(runs), len(tt.wantCount))
			}
			for i, run := range runs {
				if run.Count != tt.wantCount[i] {
					t.Fatalf("run %d count = %d, want %d", i, run.Count, tt.wantCount[i])
				}
				if run.Collapsed != tt.wantCollapsed[i] {
					t.Fatalf("run %d collapsed = %v, want %v", i, run.Collapsed, tt.wantCollapsed[i])
				}
				if len(run.Cards) != run.Count {
					t.Fatalf("run %d cards = %d, want %d", i, len(run.Cards), run.Count)
				}
			}
		})
	}
}

func TestBuildDaysKeepsRunsInsideTheirDay(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	items := []model.Item{
		streamItem("a", "one.example", base),
		streamItem("b", "one.example", base.Add(-24*time.Hour)),
		streamItem("c", "one.example", base.Add(-48*time.Hour)),
	}

	days := buildDays(items, nil, "/", 3)
	if len(days) != 3 {
		t.Fatalf("days = %d, want 3", len(days))
	}
	for i, day := range days {
		if len(day.Runs) != 1 || day.Runs[0].Count != 1 || day.Runs[0].Collapsed {
			t.Fatalf("day %d runs = %#v, want one open single-item run", i, day.Runs)
		}
	}
}

func TestBuildDaysSkipsEmptyDays(t *testing.T) {
	items := []model.Item{
		streamItem("a", "one.example", time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)),
		streamItem("b", "one.example", time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)),
	}

	days := buildDays(items, nil, "/", 3)
	if len(days) != 2 {
		t.Fatalf("days = %d, want 2", len(days))
	}
	if days[0].Label != "November 24, 2026" || days[1].Label != "November 20, 2026" {
		t.Fatalf("labels = %q, %q", days[0].Label, days[1].Label)
	}
}

func TestDateRangeLabel(t *testing.T) {
	tests := []struct {
		name   string
		oldest time.Time
		newest time.Time
		want   string
	}{
		{
			name:   "same day",
			oldest: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC),
			newest: time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC),
			want:   "November 24, 2026",
		},
		{
			name:   "different days",
			oldest: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			newest: time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC),
			want:   "November 1, 2026 – November 24, 2026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dateRangeLabel(tt.oldest, tt.newest); got != tt.want {
				t.Fatalf("label = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildArchivesGroupsNewestFirst(t *testing.T) {
	items := []model.Item{
		streamItem("a", "one.example", time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC)),
		streamItem("b", "one.example", time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)),
		streamItem("c", "one.example", time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
	}

	archives := buildArchives(items)
	if len(archives) != 2 {
		t.Fatalf("years = %d, want 2", len(archives))
	}
	if archives[0].Year != 2026 || archives[1].Year != 2025 {
		t.Fatalf("year order = %d, %d", archives[0].Year, archives[1].Year)
	}
	if len(archives[0].Months) != 2 {
		t.Fatalf("2026 months = %d, want 2", len(archives[0].Months))
	}
	if archives[0].Months[0].Month != time.November || archives[0].Months[1].Month != time.September {
		t.Fatalf("month order = %v, %v", archives[0].Months[0].Month, archives[0].Months[1].Month)
	}
	if len(archives[0].Months[0].Items) != 1 {
		t.Fatalf("November items = %d, want 1", len(archives[0].Months[0].Items))
	}
}

func TestBuildStoreLinksOrdersByCountThenLabel(t *testing.T) {
	stores := []model.Store{
		{Key: "c.example", Label: "C", Path: "/store/c.example/", ItemIDs: []string{"1"}},
		{Key: "a.example", Label: "A", Path: "/store/a.example/", ItemIDs: []string{"1"}},
		{Key: "big.example", Label: "Big", Path: "/store/big.example/", ItemIDs: []string{"1", "2", "3"}},
	}

	links := buildStoreLinks(stores, "/")
	want := []string{"Big", "A", "C"}
	for i, label := range want {
		if links[i].Label != label {
			t.Fatalf("link[%d] = %q, want %q", i, links[i].Label, label)
		}
	}
	if links[0].CountLabel != "3 items" {
		t.Fatalf("first link = %#v", links[0])
	}
}
