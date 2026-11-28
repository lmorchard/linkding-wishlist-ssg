package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyncAmazonCLIValidation(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &application{
		stdout:    stdout,
		stderr:    stderr,
		lookupEnv: func(string) (string, bool) { return "", false },
		now:       time.Now,
	}

	// Missing cdp-url
	exit := app.run(context.Background(), []string{"sync-amazon", "--list-id", "TEST1234"})
	if exit != 1 {
		t.Fatalf("expected exit 1 for missing cdp-url, got %d", exit)
	}
	if !strings.Contains(stderr.String(), "--cdp-url is required") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	// Help flag
	stdout.Reset()
	stderr.Reset()
	exit = app.run(context.Background(), []string{"sync-amazon", "--help"})
	if exit != 0 {
		t.Fatalf("expected exit 0 for --help, got %d", exit)
	}
	if !strings.Contains(stdout.String(), "Usage: linkding-wishlist-ssg sync-amazon") {
		t.Fatalf("unexpected help stdout: %s", stdout.String())
	}
}

func TestSyncAmazonMissingCredentials(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "empty.env")
	if err := os.WriteFile(envPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &application{
		stdout:      stdout,
		stderr:      stderr,
		envFilePath: envPath,
		lookupEnv:   func(string) (string, bool) { return "", false },
		now:         time.Now,
	}

	exit := app.run(context.Background(), []string{"sync-amazon", "--cdp-url", "http://127.0.0.1:9222"})
	if exit != 1 {
		t.Fatalf("expected exit 1, got %d", exit)
	}
	if !strings.Contains(stderr.String(), "LINKDING_URL is required") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestSyncAmazonMockFlow(t *testing.T) {
	// Mock Linkding API
	var checkedURLs []string
	var postedURLs []string

	linkdingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/bookmarks/check/" {
			u := r.URL.Query().Get("url")
			checkedURLs = append(checkedURLs, u)
			if u == "https://www.amazon.com/dp/SEEN1" {
				fmt.Fprint(w, `{"bookmark": {"id": 1, "url": "https://www.amazon.com/dp/SEEN1"}}`)
			} else {
				fmt.Fprint(w, `{"bookmark": null}`)
			}
			return
		}
		if r.URL.Path == "/api/bookmarks/" && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id": 100, "url": "https://www.amazon.com/dp/NEW1", "title": "New Want"}`)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer linkdingServer.Close()

	// Mock CDP Browser
	cdpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/json/version" {
			// Mock version response
			fmt.Fprintf(w, `{"webSocketDebuggerUrl": "ws://%s/devtools/browser/mock"}`, r.Host)
			return
		}
	}))
	defer cdpServer.Close()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := &application{
		stdout: stdout,
		stderr: stderr,
		lookupEnv: func(key string) (string, bool) {
			switch key {
			case "LINKDING_URL":
				return linkdingServer.URL, true
			case "LINKDING_TOKEN":
				return "mock-token", true
			default:
				return "", false
			}
		},
		now: time.Now,
	}

	// Test flag parsing and initial wiring
	exit := app.run(context.Background(), []string{
		"sync-amazon",
		"--list-id", "TEST_LIST",
		"--cdp-url", cdpServer.URL,
		"--dry-run",
	})

	// Since cdpServer isn't a full WS implementation, dialer fails as expected on websocket connect
	if exit != 1 {
		t.Fatalf("expected exit 1 on mock WS dial, got %d", exit)
	}
	if !strings.Contains(stdout.String(), "sync-amazon: connecting to") {
		t.Fatalf("expected connect message in stdout: %s", stdout.String())
	}
	_ = checkedURLs
	_ = postedURLs
}
