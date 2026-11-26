// Package server provides a lightweight HTTP server for previewing static sites.
package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Options configures the preview server.
type Options struct {
	Dir  string // directory of static files to serve
	Addr string // listen address (e.g. ":8080" or "0.0.0.0:8878")
}

// Handler returns an http.Handler that serves files from dir with no-cache
// headers so previews always show updated content.
func Handler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent aggressive browser caching during dev/preview
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		fs.ServeHTTP(w, r)
	})
}

// Serve starts an HTTP static file server and blocks until ctx is cancelled or
// SIGINT/SIGTERM is received.
func Serve(ctx context.Context, opts Options, stdout, stderr io.Writer) error {
	dir := opts.Dir
	if dir == "" {
		dir = "./build"
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving site dir %q: %w", dir, err)
	}

	if info, err := os.Stat(absDir); err != nil {
		return fmt.Errorf("site dir %q: %w", absDir, err)
	} else if !info.IsDir() {
		return fmt.Errorf("site dir %q is not a directory", absDir)
	}

	addr := opts.Addr
	if addr == "" {
		addr = ":8080"
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler: Handler(absDir),
	}

	// Format a helpful display URL
	displayAddr := listener.Addr().String()
	displayURL := displayAddr
	if strings.HasPrefix(displayURL, "[::]:") {
		displayURL = "http://localhost:" + displayURL[5:]
	} else if strings.HasPrefix(displayURL, "0.0.0.0:") {
		displayURL = "http://localhost:" + displayURL[8:]
	} else if !strings.HasPrefix(displayURL, "http://") {
		displayURL = "http://" + displayURL
	}

	fmt.Fprintf(stdout, "Serving %s at %s (press Ctrl+C to stop)\n", absDir, displayURL)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-ctx.Done():
		fmt.Fprintln(stdout, "\nShutting down server (context cancelled)...")
	case sig := <-sigCh:
		fmt.Fprintf(stdout, "\nShutting down server (received %v)...\n", sig)
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
