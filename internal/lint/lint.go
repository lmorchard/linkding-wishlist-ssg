// Package lint reports items whose fit looks doubtful. Lint never suppresses
// an item and never writes anywhere but stdout.
package lint

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/identity"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

// Reason codes, in the order they are reported for an item.
const (
	CodeShallowPath      = "shallow_path"
	CodeTitleMatchesHost = "title_matches_host"
	CodeUnparseableURL   = "unparseable_url"
)

const (
	suggestionShallowPath      = "consider: is this a store, not a want?"
	suggestionTitleMatchesHost = "consider: is this a store, not a want?"
	suggestionUnparseableURL   = "consider: check the URL"
)

// Finding is one flagged item.
type Finding struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Codes       []string `json:"codes"`
	Suggestions []string `json:"suggestions"`
}

// Run evaluates every lint rule over items in build order.
func Run(items []model.Item) []Finding {
	findings := make([]Finding, 0)
	for _, item := range items {
		codes, suggestions := evaluate(item)
		if len(codes) == 0 {
			continue
		}
		findings = append(findings, Finding{
			ID:          item.ID,
			URL:         item.URL,
			Codes:       codes,
			Suggestions: suggestions,
		})
	}
	return findings
}

func evaluate(item model.Item) ([]string, []string) {
	var codes, suggestions []string
	add := func(code, suggestion string) {
		codes = append(codes, code)
		suggestions = append(suggestions, suggestion)
	}

	parsed, err := url.Parse(item.URL)
	_, storeKeyOK := identity.StoreKey(item.URL)

	if !storeKeyOK {
		add(CodeUnparseableURL, suggestionUnparseableURL)
	}

	if err == nil && storeKeyOK {
		if isShallowPath(parsed.Path) {
			add(CodeShallowPath, suggestionShallowPath)
		}
		if titleMatchesHost(item.Title, parsed.Hostname(), item.StoreKey) {
			add(CodeTitleMatchesHost, suggestionTitleMatchesHost)
		}
	}

	return codes, suggestions
}

func isShallowPath(path string) bool {
	return path == "" || path == "/"
}

func titleMatchesHost(title, hostname, storeKey string) bool {
	normalized := normalizeTitle(title)
	if normalized == "" {
		return false
	}

	host := strings.ToLower(hostname)
	host = strings.TrimPrefix(host, "www.")
	return normalized == host || normalized == strings.ToLower(storeKey)
}

func normalizeTitle(title string) string {
	normalized := strings.ToLower(strings.TrimSpace(title))
	normalized = strings.TrimPrefix(normalized, "https://")
	normalized = strings.TrimPrefix(normalized, "http://")
	normalized = strings.TrimPrefix(normalized, "www.")
	normalized = strings.TrimSuffix(normalized, "/")
	return normalized
}

// WriteText writes the human-readable report.
func WriteText(w io.Writer, findings []Finding) error {
	if _, err := fmt.Fprintf(w, "lint: %d item(s) look off\n", len(findings)); err != nil {
		return err
	}
	for _, finding := range findings {
		if _, err := fmt.Fprintf(w, "  %s  %s\n", finding.ID, finding.URL); err != nil {
			return err
		}
		for i, code := range finding.Codes {
			if _, err := fmt.Fprintf(w, "    %-20s%s\n", code, finding.Suggestions[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteJSON writes the machine-readable report.
func WriteJSON(w io.Writer, findings []Finding) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(findings)
}
