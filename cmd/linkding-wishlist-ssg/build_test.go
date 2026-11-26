package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/linkding"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/lint"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/manifest"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/store"
)

var updateGolden = flag.Bool("update", false, "update golden files")

const fixturePath = "../../testdata/bookmarks.json"

var fixedNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

type recordedRequest struct {
	path  string
	query string
}

func startFixtureServer(t *testing.T) (*httptest.Server, func() []recordedRequest) {
	t.Helper()

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture linkding.BookmarkListResponse
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	var mu sync.Mutex
	var requests []recordedRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, recordedRequest{path: r.URL.Path, query: r.URL.RawQuery})
		mu.Unlock()

		var results []linkding.Bookmark
		switch r.URL.Path {
		case "/api/bookmarks/":
			for _, bookmark := range fixture.Results {
				if !bookmark.IsArchived {
					results = append(results, bookmark)
				}
			}
		case "/api/bookmarks/archived/":
			for _, bookmark := range fixture.Results {
				if bookmark.IsArchived {
					results = append(results, bookmark)
				}
			}
		default:
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(linkding.BookmarkListResponse{
			Count:   len(results),
			Results: results,
		})
	}))
	t.Cleanup(server.Close)

	return server, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), requests...)
	}
}

func newTestApp(linkdingURL string) (*application, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return &application{
		stdout: stdout,
		stderr: stderr,
		lookupEnv: func(key string) (string, bool) {
			switch key {
			case "LINKDING_URL":
				return linkdingURL, true
			case "LINKDING_TOKEN":
				return "test-token", true
			default:
				return "", false
			}
		},
		now:           func() time.Time { return fixedNow },
		httpClient:    &http.Client{Timeout: 2 * time.Second},
		retryBackoffs: []time.Duration{},
	}, stdout, stderr
}

func runBuild(t *testing.T, app *application, args ...string) int {
	t.Helper()
	return app.run(context.Background(), append([]string{"build"}, args...))
}

func TestBuildHelpWritesToStdout(t *testing.T) {
	app, stdout, stderr := newTestApp("https://links.example.com")
	if exit := runBuild(t, app, "--help"); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if !strings.Contains(stdout.String(), "Usage: linkding-wishlist-ssg build") {
		t.Fatalf("stdout missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestBuildAgainstFixtures(t *testing.T) {
	server, requests := startFixtureServer(t)
	app, stdout, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	exit := runBuild(t, app, "--out", outDir)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}

	for _, name := range []string{
		"index.html",
		"manifest.json",
		"assets/style.css",
		"store/sevensisterspdx.com/index.html",
		"stores/index.html",
		"archive/2026/index.html",
		"archive/2026/11/index.html",
		"archive/2026/09/index.html",
		"store/bookshop.org/index.html",
		"store/example.com/index.html",
		"store/unknown/index.html",
		"facet/book/index.html",
		"facet/kitchen/index.html",
	} {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(name))); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	for _, request := range requests() {
		if request.query == "" || !strings.Contains(request.query, "q=%23wishlist") {
			t.Fatalf("request %s missing server-side tag filter: %q", request.path, request.query)
		}
	}

	indexBytes, err := os.ReadFile(filepath.Join(outDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	index := string(indexBytes)
	for _, want := range []string{
		"Seven Sisters",
		"Sardine Head Salt Bowl",
		"The Book",
		"a-book",
		"USD 42.00",
		"small, green",
		"Broken URL",
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("index missing %q", want)
		}
	}
	if strings.Contains(index, "Archived Want") {
		t.Fatal("archived bookmark rendered by default")
	}
	if !strings.Contains(index, `<script id="wishlist-data" type="application/json">`) {
		t.Fatal("index missing wishlist-data script element")
	}
	onAttribute := regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	if onAttribute.MatchString(index) {
		t.Fatal("index contains an inline event handler")
	}

	november := string(readFile(t, filepath.Join(outDir, "archive/2026/11/index.html")))
	if !strings.Contains(november, "Sardine Head Salt Bowl") {
		t.Fatalf("November archive missing November item:\n%s", november)
	}
	if strings.Contains(november, "Broken URL") {
		t.Fatalf("November archive contains a September item:\n%s", november)
	}

	stores := string(readFile(t, filepath.Join(outDir, "stores/index.html")))
	exampleStore := strings.Index(stores, "example.com")
	bookshopStore := strings.Index(stores, "bookshop.org")
	sevenSisters := strings.Index(stores, "Seven Sisters")
	if exampleStore < 0 || bookshopStore < 0 || sevenSisters < 0 {
		t.Fatalf("stores page missing stores:\n%s", stores)
	}
	if !(exampleStore < bookshopStore && bookshopStore < sevenSisters) {
		t.Fatalf("stores page order wrong:\n%s", stores)
	}
	if !strings.Contains(stores, "3 items") {
		t.Fatalf("stores page missing counts:\n%s", stores)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifestBytes, []byte("lint")) {
		t.Fatal("manifest contains lint data")
	}
	if bytes.Contains(manifestBytes, []byte("test-token")) {
		t.Fatal("manifest contains the API token")
	}

	var got manifest.Manifest
	if err := json.Unmarshal(manifestBytes, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Source.Tag != "wishlist" || got.Source.BaseURL != "/" {
		t.Fatalf("manifest header = %#v", got)
	}
	if len(got.Items) != 8 {
		t.Fatalf("manifest items = %d, want 8", len(got.Items))
	}
	if len(got.Stores) != 4 {
		t.Fatalf("manifest stores = %d, want 4", len(got.Stores))
	}
	if len(got.Facets) != 2 {
		t.Fatalf("manifest facets = %d, want 2", len(got.Facets))
	}

	wantOrder := []string{
		"https://www.sevensisterspdx.com/",
		"https://bookshop.org/books/second-priority",
		"https://www.sevensisterspdx.com/shop/sardine-head-salt-bowl",
		"https://example.com/malformed",
		"https://example.com/a-book",
		"https://bookshop.org/books/the-book",
		"http://[::1",
		"https://example.com/",
	}
	for i, want := range wantOrder {
		if got.Items[i].URL != want {
			t.Fatalf("item %d = %q, want %q", i, got.Items[i].URL, want)
		}
	}

	fiction := findItem(t, got, "https://bookshop.org/books/the-book")
	if fiction.StoreLabel != "bookshop.org" {
		t.Fatalf("store label = %q", fiction.StoreLabel)
	}
	if !equalStrings(fiction.Tags, []string{"wishlist", "wishlist:book:fiction"}) {
		t.Fatalf("tags = %v", fiction.Tags)
	}
	if !equalStrings(fiction.Facets, []string{"book"}) {
		t.Fatalf("facets = %v", fiction.Facets)
	}

	storeFront := findItem(t, got, "https://www.sevensisterspdx.com/")
	if storeFront.StoreLabel != "Seven Sisters" {
		t.Fatalf("store label = %q", storeFront.StoreLabel)
	}
	if storeFront.Overrides == nil || storeFront.Overrides.Price == nil || *storeFront.Overrides.Price != 42 {
		t.Fatalf("overrides = %#v", storeFront.Overrides)
	}
	if storeFront.Overrides.Priority == nil || *storeFront.Overrides.Priority != 1 {
		t.Fatalf("priority = %#v", storeFront.Overrides.Priority)
	}

	broken := findItem(t, got, "http://[::1")
	if broken.StoreKey != "unknown" {
		t.Fatalf("unparseable store key = %q, want unknown", broken.StoreKey)
	}

	if !strings.Contains(stderr.String(), "tier2: parse error in https://example.com/malformed") {
		t.Fatalf("stderr missing malformed-block warning:\n%s", stderr.String())
	}
	if count := strings.Count(stderr.String(), "tier2:"); count != 1 {
		t.Fatalf("tier2 warnings = %d, want 1:\n%s", count, stderr.String())
	}

	if !strings.Contains(stdout.String(), "lint: 3 item(s) look off") {
		t.Fatalf("stdout missing lint summary:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "shallow_path") {
		t.Fatalf("stdout missing lint code:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "build: 8 item(s), 4 store(s), 2 facet(s)") {
		t.Fatalf("stdout missing final summary:\n%s", stdout.String())
	}

	assertNoLintFiles(t, outDir)
}

func TestBuildIncludeArchived(t *testing.T) {
	server, requests := startFixtureServer(t)
	app, _, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir, "--include-archived"); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}

	index, err := os.ReadFile(filepath.Join(outDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "Archived Want") {
		t.Fatal("archived item missing with --include-archived")
	}
	if !strings.Contains(string(index), "card-archived") {
		t.Fatal("archived item has no visible marker")
	}

	data, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got manifest.Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 9 {
		t.Fatalf("items = %d, want 9", len(got.Items))
	}
	archived := findItem(t, got, "https://example.com/archived-want")
	if !archived.Archived {
		t.Fatal("archived flag missing from manifest")
	}

	requestedArchived := false
	for _, request := range requests() {
		if request.path == "/api/bookmarks/archived/" {
			requestedArchived = true
		}
	}
	if !requestedArchived {
		t.Fatal("archived endpoint was not requested")
	}
}

func TestBuildLoadsCredentialsFromEnvFile(t *testing.T) {
	server, _ := startFixtureServer(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "# credentials\nLINKDING_URL=" + server.URL + "\nLINKDING_TOKEN=from-file\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &application{
		stdout:        stdout,
		stderr:        stderr,
		lookupEnv:     func(string) (string, bool) { return "", false },
		envFilePath:   envPath,
		now:           func() time.Time { return fixedNow },
		httpClient:    &http.Client{Timeout: 2 * time.Second},
		retryBackoffs: []time.Duration{},
	}
	outDir := filepath.Join(dir, "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "manifest.json")); err != nil {
		t.Fatalf("build did not use .env credentials: %v", err)
	}
	if strings.Contains(stderr.String(), "error:") {
		t.Fatalf("stderr has an error:\n%s", stderr.String())
	}
}

func TestBuildProcessEnvironmentOverridesEnvFile(t *testing.T) {
	server, _ := startFixtureServer(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "LINKDING_URL=http://127.0.0.1:1\nLINKDING_TOKEN=from-file\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &application{
		stdout: stdout,
		stderr: stderr,
		lookupEnv: func(key string) (string, bool) {
			switch key {
			case "LINKDING_URL":
				return server.URL, true
			case "LINKDING_TOKEN":
				return "from-environment", true
			default:
				return "", false
			}
		},
		envFilePath:   envPath,
		now:           func() time.Time { return fixedNow },
		httpClient:    &http.Client{Timeout: 2 * time.Second},
		retryBackoffs: []time.Duration{},
	}
	outDir := filepath.Join(dir, "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
}

func TestBuildMissingEnvFileFallsBackToProcessEnvironment(t *testing.T) {
	server, _ := startFixtureServer(t)
	app, _, stderr := newTestApp(server.URL)
	app.envFilePath = filepath.Join(t.TempDir(), "absent.env")
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
}

func TestBuildUnreadableEnvFileWarnsAndUsesProcessEnvironment(t *testing.T) {
	server, _ := startFixtureServer(t)
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.Mkdir(envPath, 0o755); err != nil {
		t.Fatal(err)
	}

	app, _, stderr := newTestApp(server.URL)
	app.envFilePath = envPath
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: reading") {
		t.Fatalf("stderr missing .env read warning:\n%s", stderr.String())
	}
}

func TestBuildFailsWithoutCredentials(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantText string
	}{
		{
			name:     "missing URL",
			env:      map[string]string{"LINKDING_TOKEN": "token"},
			wantText: "LINKDING_URL is required",
		},
		{
			name:     "missing token",
			env:      map[string]string{"LINKDING_URL": "https://links.example.com"},
			wantText: "LINKDING_TOKEN is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outDir := filepath.Join(t.TempDir(), "site")
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			app := &application{
				stdout: stdout,
				stderr: stderr,
				lookupEnv: func(key string) (string, bool) {
					value, ok := tt.env[key]
					return value, ok
				},
				now: func() time.Time { return fixedNow },
			}

			if exit := runBuild(t, app, "--out", outDir); exit != 1 {
				t.Fatalf("exit = %d, want 1", exit)
			}
			if !strings.Contains(stderr.String(), tt.wantText) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantText)
			}
			if _, err := os.Stat(outDir); !os.IsNotExist(err) {
				t.Fatalf("output directory was touched: %v", err)
			}
		})
	}
}

func TestBuildFailsOnUnreachableLinkdingWithoutPartialOutput(t *testing.T) {
	app, _, stderr := newTestApp("http://127.0.0.1:1")
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr.String(), "error: fetching bookmarks") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("partial output exists after failure: %v", err)
	}
}

func TestBuildMalformedTier2WarnsAndRenders(t *testing.T) {
	server := bookmarkServer(t, []linkding.Bookmark{{
		ID:        1,
		URL:       "https://example.com/thing",
		Title:     "A Thing",
		Notes:     "```wishlist\nstore: [broken\n```",
		TagNames:  []string{"wishlist", "wishlist:book"},
		DateAdded: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}})
	app, _, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
	if count := strings.Count(stderr.String(), "tier2:"); count != 1 {
		t.Fatalf("warnings = %d, want 1:\n%s", count, stderr.String())
	}
	index, err := os.ReadFile(filepath.Join(outDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "A Thing") {
		t.Fatal("item with malformed override did not render")
	}
}

func TestBuildEmptyTitleFallsBackToURL(t *testing.T) {
	server := bookmarkServer(t, []linkding.Bookmark{{
		ID:        1,
		URL:       "https://example.com/a-book",
		Title:     "",
		TagNames:  []string{"wishlist", "wishlist:book"},
		DateAdded: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}})
	app, _, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
	index, err := os.ReadFile(filepath.Join(outDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), ">a-book<") {
		t.Fatalf("URL-derived title missing:\n%s", index)
	}
}

func TestBuildMakesNoTargetPageRequests(t *testing.T) {
	var hits int
	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	server := bookmarkServer(t, []linkding.Bookmark{{
		ID:        1,
		URL:       target.URL + "/product/thing",
		Title:     "Targeted Thing",
		TagNames:  []string{"wishlist", "wishlist:book"},
		DateAdded: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}})
	app, _, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}

	mu.Lock()
	gotHits := hits
	mu.Unlock()
	if gotHits != 0 {
		t.Fatalf("target page was fetched %d time(s); v0 must only call Linkding", gotHits)
	}
}

func TestBuildLintJSON(t *testing.T) {
	server, _ := startFixtureServer(t)
	app, stdout, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir, "--lint-format=json"); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}

	output := stdout.String()
	start := strings.Index(output, "[")
	if start < 0 {
		t.Fatalf("no JSON lint report in stdout:\n%s", output)
	}
	var findings []lint.Finding
	decoder := json.NewDecoder(strings.NewReader(output[start:]))
	if err := decoder.Decode(&findings); err != nil {
		t.Fatalf("decoding lint JSON: %v\n%s", err, output)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %d, want 3", len(findings))
	}
}

func TestBuildNoLint(t *testing.T) {
	server, _ := startFixtureServer(t)
	app, stdout, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir, "--no-lint"); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
	if strings.Contains(stdout.String(), "lint:") {
		t.Fatalf("lint report printed despite --no-lint:\n%s", stdout.String())
	}
}

func TestBuildIsStableAcrossRuns(t *testing.T) {
	server, _ := startFixtureServer(t)
	outOne := filepath.Join(t.TempDir(), "one")
	outTwo := filepath.Join(t.TempDir(), "two")

	firstApp, _, firstErr := newTestApp(server.URL)
	firstApp.now = func() time.Time { return fixedNow }
	if exit := runBuild(t, firstApp, "--out", outOne); exit != 0 {
		t.Fatalf("first build exit = %d, stderr:\n%s", exit, firstErr.String())
	}

	secondApp, _, secondErr := newTestApp(server.URL)
	secondApp.now = func() time.Time { return fixedNow.Add(time.Hour) }
	if exit := runBuild(t, secondApp, "--out", outTwo); exit != 0 {
		t.Fatalf("second build exit = %d, stderr:\n%s", exit, secondErr.String())
	}

	firstManifest := readFile(t, filepath.Join(outOne, "manifest.json"))
	secondManifest := readFile(t, filepath.Join(outTwo, "manifest.json"))
	normalizedFirst := normalizeGeneratedAt(t, firstManifest)
	normalizedSecond := normalizeGeneratedAt(t, secondManifest)
	if !bytes.Equal(normalizedFirst, normalizedSecond) {
		t.Fatalf("manifests differ beyond generated_at:\n%s\n---\n%s", normalizedFirst, normalizedSecond)
	}

	if first, second := readFile(t, filepath.Join(outOne, "index.html")), readFile(t, filepath.Join(outTwo, "index.html")); !bytes.Equal(first, second) {
		t.Fatal("index.html differs between runs")
	}
}

func TestBuildWipesStaleOutput(t *testing.T) {
	server, _ := startFixtureServer(t)
	app, _, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(outDir, "stale.html")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale output survived: %v", err)
	}
}

func TestGoldenManifestAndStorePage(t *testing.T) {
	server, _ := startFixtureServer(t)
	app, _, stderr := newTestApp(server.URL)
	outDir := filepath.Join(t.TempDir(), "site")

	if exit := runBuild(t, app, "--out", outDir); exit != 0 {
		t.Fatalf("exit = %d, stderr:\n%s", exit, stderr.String())
	}

	compareGolden(t, filepath.Join(outDir, "manifest.json"), "../../testdata/golden/manifest.json")
	compareGolden(t, filepath.Join(outDir, "index.html"), "../../testdata/golden/index.html")
	compareGolden(t,
		filepath.Join(outDir, "store/sevensisterspdx.com/index.html"),
		"../../testdata/golden/store-sevensisterspdx.com.html")
}

func compareGolden(t *testing.T, gotPath, goldenPath string) {
	t.Helper()
	got := readFile(t, gotPath)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run go test ./cmd/... -update)", goldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from golden %s\n--- got ---\n%s\n--- want ---\n%s", gotPath, goldenPath, got, want)
	}
}

func bookmarkServer(t *testing.T, bookmarks []linkding.Bookmark) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/bookmarks/":
			_ = json.NewEncoder(w).Encode(linkding.BookmarkListResponse{Count: len(bookmarks), Results: bookmarks})
		case "/api/bookmarks/archived/":
			_ = json.NewEncoder(w).Encode(linkding.BookmarkListResponse{Count: 0, Results: []linkding.Bookmark{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func findItem(t *testing.T, m manifest.Manifest, url string) manifest.Item {
	t.Helper()
	for _, item := range m.Items {
		if item.URL == url {
			return item
		}
	}
	t.Fatalf("manifest has no item with URL %q", url)
	return manifest.Item{}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func assertNoLintFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.Contains(strings.ToLower(info.Name()), "lint") {
			t.Fatalf("lint artifact written to disk: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var generatedAtPattern = regexp.MustCompile(`"generated_at": "[^"]*"`)

func normalizeGeneratedAt(t *testing.T, data []byte) []byte {
	t.Helper()
	replaced := generatedAtPattern.ReplaceAll(data, []byte(`"generated_at": "FIXED"`))
	if bytes.Equal(replaced, data) {
		t.Fatal("generated_at not found in manifest")
	}
	return replaced
}

func TestApplyObservedPricesAndMetadata(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wishlist.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	itemsToSeed := []store.Item{
		{ItemID: "i1", URL: "https://shop.example/1", Host: "shop.example", AddedAt: now},
		{ItemID: "i2", URL: "https://shop.example/2", Host: "shop.example", AddedAt: now},
	}
	if _, err := store.Ingest(db, itemsToSeed, now); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	price1 := 42.50
	avail1 := true
	obs1 := store.Observation{
		ItemID:      "i1",
		ObservedAt:  now,
		Price:       &price1,
		Currency:    "USD",
		Available:   &avail1,
		Source:      "jsonld",
		Status:      "priced",
		ImageURL:    "https://shop.example/i1.jpg",
		Description: "A great item",
		RawPayload:  "{\"name\":\"i1\"}",
	}
	if err := store.Record(db, obs1, store.OutcomePriced, now); err != nil {
		t.Fatalf("Record: %v", err)
	}

	app := &application{}
	modelItems := []model.Item{
		{
			ID:          "i1",
			URL:         "https://shop.example/1",
			Title:       "Item 1",
			Description: "", // empty, should be overlaid
		},
		{
			ID:          "i2",
			URL:         "https://shop.example/2",
			Title:       "Item 2",
			Description: "Existing description",
		},
	}

	priced, unavailable := app.applyObservedPrices(modelItems, dbPath)
	if priced != 1 {
		t.Errorf("priced = %d, want 1", priced)
	}
	if unavailable != 0 {
		t.Errorf("unavailable = %d, want 0", unavailable)
	}

	// Verify modelItems[0] got price, image, and description
	if modelItems[0].Overrides.Price == nil || *modelItems[0].Overrides.Price != 42.50 {
		t.Errorf("item 0 price = %v, want 42.50", modelItems[0].Overrides.Price)
	}
	if modelItems[0].ImageURL != "https://shop.example/i1.jpg" {
		t.Errorf("item 0 ImageURL = %q, want 'https://shop.example/i1.jpg'", modelItems[0].ImageURL)
	}
	if modelItems[0].Description != "A great item" {
		t.Errorf("item 0 Description = %q, want 'A great item'", modelItems[0].Description)
	}

	// Verify modelItems[1] was untouched
	if modelItems[1].Description != "Existing description" {
		t.Errorf("item 1 Description = %q, want 'Existing description'", modelItems[1].Description)
	}
}
