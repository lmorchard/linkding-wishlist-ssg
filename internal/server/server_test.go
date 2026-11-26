package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHandlerServesFileWithNoCacheHeaders(t *testing.T) {
	dir := t.TempDir()
	indexFile := filepath.Join(dir, "index.html")
	content := "<html><body><h1>Hello Wishlist</h1></body></html>"
	if err := os.WriteFile(indexFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	handler := Handler(dir)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify no-cache headers
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-cache") || !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-cache, no-store", cc)
	}
	if rec.Header().Get("Pragma") != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", rec.Header().Get("Pragma"))
	}
	if rec.Header().Get("Expires") != "0" {
		t.Errorf("Expires = %q, want 0", rec.Header().Get("Expires"))
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Hello Wishlist") {
		t.Errorf("body = %q, want Hello Wishlist", body)
	}
}

func TestServeGracefulShutdownOnCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	opts := Options{
		Dir:  dir,
		Addr: "127.0.0.1:0", // random available port
	}

	var stdout, stderr strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, opts, &stdout, &stderr)
	}()

	// Wait briefly for server to spin up
	time.Sleep(100 * time.Millisecond)

	// Trigger cancellation
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve timed out waiting for shutdown")
	}

	if !strings.Contains(stdout.String(), "Serving") {
		t.Errorf("stdout missing 'Serving': %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Shutting down server") {
		t.Errorf("stdout missing 'Shutting down': %s", stdout.String())
	}
}
