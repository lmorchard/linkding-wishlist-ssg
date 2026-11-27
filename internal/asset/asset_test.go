package asset

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

func TestFilenameFor(t *testing.T) {
	tests := []struct {
		url     string
		wantExt string
	}{
		{"https://example.com/photo.png", ".png"},
		{"https://example.com/art.webp?w=400", ".webp"},
		{"https://example.com/pic.JPEG", ".jpeg"},
		{"https://example.com/vector.svg#icon", ".svg"},
		{"https://m.media-amazon.com/images/I/71xyz._AC_.jpg", ".jpg"},
		{"https://example.com/image-with-no-ext", ".jpg"},
	}
	for _, tt := range tests {
		fn := FilenameFor(tt.url)
		if len(fn) < 16+len(tt.wantExt) {
			t.Errorf("FilenameFor(%q) = %q, too short", tt.url, fn)
		}
		if !strings.HasSuffix(fn, tt.wantExt) {
			t.Errorf("FilenameFor(%q) = %q, want extension %s", tt.url, fn, tt.wantExt)
		}
	}
}

func TestCacheThumbnailsDownloadsAndCaches(t *testing.T) {
	// Mock image server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail.jpg" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer ts.Close()

	dir := t.TempDir()
	thumbsDir := filepath.Join(dir, "thumbs")

	items := []model.Item{
		{
			ID:       "item1",
			ImageURL: ts.URL + "/good.png",
		},
		{
			ID:       "item2",
			ImageURL: ts.URL + "/fail.jpg",
		},
		{
			ID:       "item3",
			ImageURL: "", // empty
		},
	}

	opts := Options{
		ThumbsDir:   thumbsDir,
		URLPrefix:   "/custom-thumbs/",
		Concurrency: 2,
		Timeout:     2 * time.Second,
		HTTPClient:  ts.Client(),
	}

	count, errs := CacheThumbnails(context.Background(), items, opts)
	if count != 1 {
		t.Errorf("cachedCount = %d, want 1", count)
	}
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d", len(errs))
	}

	// Verify item1 updated to local URL
	if !strings.HasPrefix(items[0].ImageURL, "/custom-thumbs/") || !strings.HasSuffix(items[0].ImageURL, ".png") {
		t.Errorf("item1 ImageURL = %q, want local /custom-thumbs/*.png", items[0].ImageURL)
	}

	// Verify file exists on disk
	fn := FilenameFor(ts.URL + "/good.png")
	diskFile := filepath.Join(thumbsDir, fn)
	data, err := os.ReadFile(diskFile)
	if err != nil {
		t.Fatalf("reading disk thumb: %v", err)
	}
	if string(data) != "fake-jpeg-bytes" {
		t.Errorf("disk content = %q, want 'fake-jpeg-bytes'", string(data))
	}

	// Verify item2 failed open and retains remote URL
	if items[1].ImageURL != ts.URL+"/fail.jpg" {
		t.Errorf("item2 ImageURL = %q, want remote URL preserved", items[1].ImageURL)
	}

	// Test cache hit: running again should not re-download
	itemsRepeat := []model.Item{
		{
			ID:       "item1",
			ImageURL: ts.URL + "/good.png",
		},
	}
	count2, errs2 := CacheThumbnails(context.Background(), itemsRepeat, opts)
	if count2 != 1 || len(errs2) != 0 {
		t.Errorf("repeat run: count = %d (want 1), errs = %d (want 0)", count2, len(errs2))
	}
}
