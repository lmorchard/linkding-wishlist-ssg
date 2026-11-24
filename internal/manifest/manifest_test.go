package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func TestBuildOmitsEmptyOverridesAndLintData(t *testing.T) {
	generated := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	dataset := model.Dataset{
		Items: []model.Item{
			{
				ID:         "a1b2c3d4e5f6",
				URL:        "https://example.com/thing",
				Title:      "Thing",
				Tags:       []string{"wishlist"},
				Facets:     []string{},
				StoreKey:   "example.com",
				StoreLabel: "example.com",
				AddedAt:    generated,
				ModifiedAt: generated,
			},
		},
	}

	got := Build(dataset, Options{Tag: "wishlist", BaseURL: "/", GeneratedAt: generated})

	var buf bytes.Buffer
	if err := Write(&buf, got); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), "overrides") {
		t.Fatalf("manifest contains overrides key for empty overrides:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "lint") {
		t.Fatalf("manifest contains lint data:\n%s", buf.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if decoded["schema_version"].(float64) != 1 {
		t.Fatalf("schema_version = %v", decoded["schema_version"])
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatal("manifest does not end with a newline")
	}
}

func TestBuildIncludesPresentOverridesOnly(t *testing.T) {
	priority := 1
	dataset := model.Dataset{
		Items: []model.Item{
			{
				ID:        "a1b2c3d4e5f6",
				URL:       "https://example.com/thing",
				Tags:      []string{"wishlist"},
				Facets:    []string{},
				Overrides: model.Overrides{Priority: &priority},
			},
		},
	}

	got := Build(dataset, Options{Tag: "wishlist", BaseURL: "/", GeneratedAt: time.Now()})
	if got.Items[0].Overrides == nil {
		t.Fatal("overrides missing")
	}
	if got.Items[0].Overrides.Priority == nil || *got.Items[0].Overrides.Priority != 1 {
		t.Fatalf("priority = %v", got.Items[0].Overrides.Priority)
	}
	if got.Items[0].Overrides.Store != nil {
		t.Fatal("absent store override was populated")
	}
}
