// Package tier2 parses the optional ```wishlist fenced block from bookmark
// notes. A malformed block never fails the build; it yields warnings and the
// item renders at Tier 0/1.
package tier2

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lmorchard/linkding-wishlist-ssg/internal/model"
)

// Parser carries per-build state, notably the image warning that is emitted
// only once no matter how many items request an image.
type Parser struct {
	imageWarned bool
}

// NewParser returns a Parser for one build.
func NewParser() *Parser {
	return &Parser{}
}

// Parse extracts overrides from notes. Warnings are already prefixed with
// "tier2:" and include rawURL.
func (p *Parser) Parse(rawURL, notes string) (model.Overrides, []string) {
	body, found, unterminated := findWishlistBlock(notes)
	if !found {
		return model.Overrides{}, nil
	}
	if unterminated {
		return model.Overrides{}, []string{
			fmt.Sprintf("tier2: parse error in %s: unterminated wishlist block", rawURL),
		}
	}
	if strings.TrimSpace(body) == "" {
		return model.Overrides{}, nil
	}

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(body), &raw); err != nil {
		return model.Overrides{}, []string{
			fmt.Sprintf("tier2: parse error in %s: %v", rawURL, err),
		}
	}
	if raw == nil {
		return model.Overrides{}, nil
	}

	var warnings []string
	var overrides model.Overrides

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := raw[key]
		switch key {
		case "store":
			s, ok := value.(string)
			if !ok {
				warnings = append(warnings, wrongType(key, rawURL))
				continue
			}
			overrides.Store = &s
		case "priority":
			n, ok := value.(int)
			if !ok {
				warnings = append(warnings, wrongType(key, rawURL))
				continue
			}
			overrides.Priority = &n
		case "price":
			switch v := value.(type) {
			case int:
				f := float64(v)
				overrides.Price = &f
			case float64:
				f := v
				overrides.Price = &f
			default:
				warnings = append(warnings, wrongType(key, rawURL))
			}
		case "currency":
			s, ok := value.(string)
			if !ok {
				warnings = append(warnings, wrongType(key, rawURL))
				continue
			}
			overrides.Currency = &s
		case "variant":
			s, ok := value.(string)
			if !ok {
				warnings = append(warnings, wrongType(key, rawURL))
				continue
			}
			overrides.Variant = &s
		case "image":
			s, ok := value.(string)
			if !ok {
				warnings = append(warnings, wrongType(key, rawURL))
				continue
			}
			overrides.Image = &s
			if !p.imageWarned {
				p.imageWarned = true
				warnings = append(warnings, fmt.Sprintf(
					"tier2: image is not rendered in v0 (image key in %s)", rawURL))
			}
		default:
			warnings = append(warnings, fmt.Sprintf(
				"tier2: unknown key %q in %s", key, rawURL))
		}
	}

	if overrides.Price != nil && (overrides.Currency == nil || *overrides.Currency == "") {
		warnings = append(warnings, fmt.Sprintf(
			"tier2: price without currency in %s", rawURL))
		overrides.Price = nil
	}

	return overrides, warnings
}

func wrongType(key, rawURL string) string {
	return fmt.Sprintf("tier2: wrong type for %q in %s", key, rawURL)
}

type fence struct {
	char   byte
	length int
	info   string
}

// findWishlistBlock locates the first fenced block whose info string is
// exactly "wishlist" (case-insensitive). It returns the block body and
// whether the opening fence was found. If the block is not closed before EOF,
// unterminated is true.
func findWishlistBlock(notes string) (body string, found bool, unterminated bool) {
	lines := strings.Split(notes, "\n")

	for i, line := range lines {
		opening, ok := parseFence(line)
		if !ok || !strings.EqualFold(opening.info, "wishlist") {
			continue
		}

		for j := i + 1; j < len(lines); j++ {
			if isClosingFence(lines[j], opening) {
				return strings.Join(lines[i+1:j], "\n"), true, false
			}
		}
		return "", true, true
	}

	return "", false, false
}

func parseFence(line string) (fence, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < 3 {
		return fence{}, false
	}

	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return fence{}, false
	}

	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return fence{}, false
	}

	return fence{
		char:   ch,
		length: n,
		info:   strings.TrimSpace(trimmed[n:]),
	}, true
}

func isClosingFence(line string, opening fence) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < opening.length {
		return false
	}
	for i := 0; i < opening.length; i++ {
		if trimmed[i] != opening.char {
			return false
		}
	}
	return strings.TrimSpace(trimmed[opening.length:]) == ""
}
