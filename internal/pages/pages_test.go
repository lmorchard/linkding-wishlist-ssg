package pages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPages(t *testing.T) {
	dir := t.TempDir()

	aboutMD := `---
title: "About Me"
---
Hello, this is about me.
`
	if err := os.WriteFile(filepath.Join(dir, "about.md"), []byte(aboutMD), 0o644); err != nil {
		t.Fatal(err)
	}

	colophonMD := `# Colophon

This site was built with Go and HTML.
`
	if err := os.WriteFile(filepath.Join(dir, "colophon.md"), []byte(colophonMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-markdown file should be ignored
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("loaded = %d pages, want 2", len(loaded))
	}

	// Alphabetical order: About Me, then Colophon
	if loaded[0].Title != "About Me" || loaded[0].Slug != "about" || loaded[0].Path != "about/" {
		t.Errorf("unexpected loaded[0]: %+v", loaded[0])
	}
	if !strings.Contains(string(loaded[0].ContentHTML), "Hello, this is about me.") {
		t.Errorf("content missing in loaded[0]: %s", loaded[0].ContentHTML)
	}

	if loaded[1].Title != "Colophon" || loaded[1].Slug != "colophon" || loaded[1].Path != "colophon/" {
		t.Errorf("unexpected loaded[1]: %+v", loaded[1])
	}
	if !strings.Contains(string(loaded[1].ContentHTML), "This site was built with Go and HTML.") {
		t.Errorf("content missing in loaded[1]: %s", loaded[1].ContentHTML)
	}
}

func TestLoadMissingDir(t *testing.T) {
	loaded, err := Load("/nonexistent/directory")
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 pages, got %d", len(loaded))
	}
}
