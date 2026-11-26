package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunServeInvalidDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := &application{
		stdout:    &stdout,
		stderr:    &stderr,
		lookupEnv: os.LookupEnv,
		now:       time.Now,
	}

	code := app.runServe(context.Background(), []string{"--dir", "/path/does/not/exist/ever"})
	if code != 1 {
		t.Fatalf("runServe exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "site dir") {
		t.Errorf("stderr = %q, want mention of site dir", stderr.String())
	}
}

func TestRunServeGraceful(t *testing.T) {
	dir := t.TempDir()
	indexFile := filepath.Join(dir, "index.html")
	_ = os.WriteFile(indexFile, []byte("<h1>test</h1>"), 0644)

	var stdout, stderr bytes.Buffer
	app := &application{
		stdout:    &stdout,
		stderr:    &stderr,
		lookupEnv: os.LookupEnv,
		now:       time.Now,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)

	go func() {
		done <- app.runServe(ctx, []string{"--dir", dir, "--addr", "127.0.0.1:0"})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("runServe returned code = %d, want 0; stderr: %s", code, stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for runServe to exit")
	}

	if !strings.Contains(stdout.String(), "Serving") {
		t.Errorf("stdout = %q, want 'Serving'", stdout.String())
	}
}
