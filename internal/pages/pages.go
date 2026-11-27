package pages

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// CustomPage represents a local Markdown file rendered as a standalone HTML page.
type CustomPage struct {
	Slug        string        // e.g. "colophon"
	Title       string        // e.g. "Colophon"
	Path        string        // relative URL, e.g. "colophon/"
	ContentHTML template.HTML // rendered safe HTML
}

var mdParser = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(),
	),
)

// Load reads all .md markdown files from dir.
// If dir does not exist or has no markdown files, it returns an empty slice and nil error.
func Load(dir string) ([]CustomPage, error) {
	if dir == "" {
		return nil, nil
	}

	stat, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checking pages dir: %w", err)
	}
	if !stat.IsDir() {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading pages dir: %w", err)
	}

	var loaded []CustomPage
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading page %s: %w", entry.Name(), err)
		}

		baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		slug := strings.ToLower(strings.TrimSpace(baseName))

		title, body := extractTitleAndBody(string(raw), baseName)

		var htmlBuf bytes.Buffer
		if err := mdParser.Convert([]byte(body), &htmlBuf); err != nil {
			return nil, fmt.Errorf("rendering markdown for %s: %w", entry.Name(), err)
		}

		loaded = append(loaded, CustomPage{
			Slug:        slug,
			Title:       title,
			Path:        slug + "/",
			ContentHTML: template.HTML(htmlBuf.String()),
		})
	}

	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].Title < loaded[j].Title
	})

	return loaded, nil
}

// extractTitleAndBody parses optional frontmatter or leading # Heading 1 as title.
func extractTitleAndBody(content, fallbackName string) (string, string) {
	lines := strings.Split(content, "\n")
	title := ""
	bodyLines := []string{}

	inFrontmatter := false
	frontmatterDone := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for YAML frontmatter delimiters: ---
		if i == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter {
			if trimmed == "---" {
				inFrontmatter = false
				frontmatterDone = true
				continue
			}
			if strings.HasPrefix(trimmed, "title:") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
				title = strings.Trim(val, "\"'")
			}
			continue
		}

		// If frontmatter didn't specify title, look for first # Heading 1
		if title == "" && strings.HasPrefix(trimmed, "# ") && !frontmatterDone {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}

		bodyLines = append(bodyLines, line)
	}

	if title == "" {
		if len(fallbackName) > 0 {
			title = strings.ToUpper(fallbackName[:1]) + fallbackName[1:]
		} else {
			title = "Page"
		}
	}

	return title, strings.Join(bodyLines, "\n")
}
