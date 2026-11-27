// Package asset handles downloading and local caching of remote image assets.
package asset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

// Options configures thumbnail downloading and caching.
type Options struct {
	ThumbsDir   string        // Target directory on disk, e.g. "dist/thumbs"
	URLPrefix   string        // Relative or absolute URL prefix, e.g. "/thumbs/"
	Concurrency int           // Parallel worker count (default 5)
	Timeout     time.Duration // Per-request HTTP timeout (default 10s)
	HTTPClient  *http.Client  // Optional HTTP client for tests
}

// DownloadMap records the mapping of remote URL to local relative URL and target filename.
type DownloadMap map[string]string // remoteURL -> localURL

// Result is the result of caching an asset.
type Result struct {
	RemoteURL string
	LocalPath string // Relative URL path, e.g. "/thumbs/a1b2c3d4.jpg"
	Cached    bool   // True if fetched or already on disk
	Err       error
}

// FilenameFor derives a stable, safe filename for a remote image URL using
// the first 16 characters of its SHA256 hash plus extension.
func FilenameFor(rawURL string) string {
	h := sha256.Sum256([]byte(rawURL))
	hashStr := hex.EncodeToString(h[:])[:16]

	ext := ".jpg"
	if u, err := url.Parse(rawURL); err == nil {
		pathExt := strings.ToLower(filepath.Ext(u.Path))
		switch pathExt {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif", ".svg":
			ext = pathExt
		}
	}
	return hashStr + ext
}

// PrepareThumbnails downloads missing images referenced by items into
// opts.ThumbsDir. It returns the download mapping (remoteURL -> localURL) and
// the count of files cached.
func PrepareThumbnails(ctx context.Context, items []model.Item, opts Options) (DownloadMap, int, []error) {
	if strings.TrimSpace(opts.ThumbsDir) == "" {
		return nil, 0, nil
	}
	if err := os.MkdirAll(opts.ThumbsDir, 0o755); err != nil {
		return nil, 0, []error{fmt.Errorf("creating thumbs directory: %w", err)}
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 5
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	urlPrefix := opts.URLPrefix
	if urlPrefix == "" {
		urlPrefix = "/thumbs/"
	}
	if !strings.HasSuffix(urlPrefix, "/") {
		urlPrefix += "/"
	}

	// 1. Collect unique remote URLs
	type downloadTask struct {
		remoteURL string
		filename  string
		destPath  string
		localURL  string
	}

	tasksByURL := make(map[string]downloadTask)
	for _, it := range items {
		if strings.TrimSpace(it.ImageURL) == "" {
			continue
		}
		// Skip if already a local/relative URL
		if strings.HasPrefix(it.ImageURL, "/") {
			continue
		}
		if _, exists := tasksByURL[it.ImageURL]; exists {
			continue
		}
		fn := FilenameFor(it.ImageURL)
		tasksByURL[it.ImageURL] = downloadTask{
			remoteURL: it.ImageURL,
			filename:  fn,
			destPath:  filepath.Join(opts.ThumbsDir, fn),
			localURL:  urlPrefix + fn,
		}
	}

	if len(tasksByURL) == 0 {
		return nil, 0, nil
	}

	// 2. Filter tasks: skip files already present on disk
	var needed []downloadTask
	localMap := make(DownloadMap)
	cachedCount := 0
	var errors []error

	for _, task := range tasksByURL {
		if stat, err := os.Stat(task.destPath); err == nil && stat.Size() > 0 {
			localMap[task.remoteURL] = task.localURL
			cachedCount++
			continue
		}
		needed = append(needed, task)
	}

	// 3. Download missing thumbnails concurrently
	if len(needed) > 0 {
		taskCh := make(chan downloadTask, len(needed))
		for _, task := range needed {
			taskCh <- task
		}
		close(taskCh)

		var wg sync.WaitGroup
		var mu sync.Mutex

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for task := range taskCh {
					err := fetchToFile(ctx, client, task.remoteURL, task.destPath)
					mu.Lock()
					if err == nil {
						localMap[task.remoteURL] = task.localURL
						cachedCount++
					} else {
						errors = append(errors, fmt.Errorf("caching %s: %w", task.remoteURL, err))
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
	}

	return localMap, cachedCount, errors
}

// ApplyThumbnails updates items' ImageURL fields using the provided mapping.
func ApplyThumbnails(items []model.Item, mapping DownloadMap) {
	if len(mapping) == 0 {
		return
	}
	for i := range items {
		if local, ok := mapping[items[i].ImageURL]; ok {
			items[i].ImageURL = local
		}
	}
}

// CacheThumbnails downloads missing images referenced by items into opts.ThumbsDir
// and updates the items in-place to point to the local opts.URLPrefix + filename.
// If an image fails to download, it leaves item.ImageURL pointing to the remote
// URL so the site fails open.
func CacheThumbnails(ctx context.Context, items []model.Item, opts Options) (cachedCount int, errors []error) {
	mapping, count, errs := PrepareThumbnails(ctx, items, opts)
	ApplyThumbnails(items, mapping)
	return count, errs
}

func fetchToFile(ctx context.Context, client *http.Client, rawURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmpFile := destPath + ".tmp"
	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	// Limit thumbnail size to 10MB
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 10<<20))
	closeErr := f.Close()

	if copyErr != nil {
		_ = os.Remove(tmpFile)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpFile)
		return closeErr
	}

	return os.Rename(tmpFile, destPath)
}
