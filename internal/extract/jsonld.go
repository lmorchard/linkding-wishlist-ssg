// Package extract pulls a price from an already-fetched HTML document.
//
// Rung 1 of the ladder: JSON-LD only. No CSS selectors, no browser. When a page
// has well-formed structured data this answers cheaply and reliably; when it
// does not, the answer is explicitly "no data" rather than a guess.
//
// The parsers walk structures instead of assuming shapes, because real JSON-LD
// nests Product under @graph, wraps Offer in an array, and varies in ways that
// a fixed path would silently miss.
package extract

import (
	"encoding/json"
	"errors"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Availability is the structured out-of-stock signal, if the page published
// one.
//
// Only schema.org's own vocabulary counts. Measured on this wishlist's hosts:
// every page whose structured data says InStock ALSO contains the literal words
// "sold out" or "out of stock" somewhere — recommendation widgets, related
// products, boilerplate. Matching that text marked 10 in-stock hosts as
// unavailable, so text is not consulted at all here.
type Availability string

const (
	// AvailabilityUnknown means the page published no availability value.
	AvailabilityUnknown Availability = ""
	// AvailabilityInStock means buyable.
	AvailabilityInStock Availability = "InStock"
	// AvailabilityOutOfStock means the shop says it cannot be bought.
	AvailabilityOutOfStock Availability = "OutOfStock"
	// AvailabilityDiscontinued means the shop says it will not return.
	AvailabilityDiscontinued Availability = "Discontinued"
)

// Unavailable reports whether this signal means "do not show this on the live
// list". InStock and Unknown both mean keep it.
func (a Availability) Unavailable() bool {
	return a == AvailabilityOutOfStock || a == AvailabilityDiscontinued
}

// Result is what extraction learned. A Result with Status "no_data" is a real
// answer: the page loaded and carried no usable offer.
type Result struct {
	Status       string // "priced" or "no_data"
	Price        *float64
	Currency     string
	Available    *bool
	Availability Availability
	Source       string
	Detail       string
	ImageURL     string
	Description  string
	RawPayload   string
}

// ErrNoDocument means the caller passed nothing to parse, which is a programming
// error rather than a page property.
var ErrNoDocument = errors.New("extract: empty document")

var (
	scriptTag   = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']application/ld\+json["'][^>]*>(.*?)</script>`)
	jsonComment = regexp.MustCompile(`(?m)^\s*//.*$`)
)

// JSONLD is the rung-1 extractor. The parameter is the page's HTML.
func JSONLD(document string) (Result, error) {
	if strings.TrimSpace(document) == "" {
		return Result{}, ErrNoDocument
	}

	blocks := scriptTag.FindAllStringSubmatch(document, -1)
	if len(blocks) == 0 {
		return Result{Status: "no_data", Source: "jsonld", Detail: "no ld+json block"}, nil
	}

	for _, b := range blocks {
		raw := strings.TrimSpace(html.UnescapeString(b[1]))
		if raw == "" {
			continue
		}
		// Some sites emit JS comments inside the block, which is invalid JSON
		// but common enough to be worth stripping before giving up.
		raw = jsonComment.ReplaceAllString(raw, "")

		var doc any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			continue
		}
		if r, ok := findOffer(doc); ok {
			r.Source = "jsonld"
			r.RawPayload = raw
			return r, nil
		}
	}

	return Result{Status: "no_data", Source: "jsonld", Detail: "no Product offer"}, nil
}

// findOffer walks any JSON shape looking for a Product with an Offer.
func findOffer(node any) (Result, bool) {
	switch v := node.(type) {
	case []any:
		for _, elem := range v {
			if r, ok := findOffer(elem); ok {
				return r, true
			}
		}
	case map[string]any:
		if isProduct(v) {
			if r, ok := offerFromProduct(v); ok {
				return r, true
			}
		}
		// @graph and similar containers hold the real nodes one level down.
		for _, key := range []string{"@graph", "mainEntity", "itemListElement", "item"} {
			if child, present := v[key]; present {
				if r, ok := findOffer(child); ok {
					return r, true
				}
			}
		}
	}
	return Result{}, false
}

func isProduct(m map[string]any) bool {
	t := typeStrings(m["@type"])
	for _, s := range t {
		if strings.EqualFold(s, "Product") {
			return true
		}
	}
	// Some shops omit @type but still publish offers, which is the part we
	// actually need.
	return m["offers"] != nil && m["@type"] == nil
}

func typeStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func offerFromProduct(p map[string]any) (Result, bool) {
	offers := p["offers"]
	if offers == nil {
		return Result{}, false
	}

	imageURL := imageOf(p)
	desc, _ := p["description"].(string)

	for _, o := range flatten(offers) {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		price, currency, ok := priceOf(m)
		if !ok {
			continue
		}
		r := Result{
			Status:      "priced",
			Price:       &price,
			ImageURL:    imageURL,
			Description: strings.TrimSpace(desc),
		}
		// If offer itself has an image, let that override product image
		if offerImg := imageOf(m); offerImg != "" {
			r.ImageURL = offerImg
		}
		// The offer's own currency wins; the specification's is a fallback
		// because some shops put it only there.
		if s, ok := m["priceCurrency"].(string); ok {
			currency = s
		}
		r.Currency = strings.ToUpper(strings.TrimSpace(currency))
		if s, ok := m["availability"].(string); ok {
			r.Availability = parseAvailability(s)
			avail := !r.Availability.Unavailable()
			r.Available = &avail
		}
		return r, true
	}
	return Result{}, false
}

// imageOf extracts a single image URL from an object, which schema.org defines
// as a string, a list of strings, or an ImageObject with a url field.
func imageOf(m map[string]any) string {
	val := m["image"]
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return strings.TrimSpace(s)
			}
			if obj, ok := v[0].(map[string]any); ok {
				if s, ok := obj["url"].(string); ok {
					return strings.TrimSpace(s)
				}
			}
		}
	case map[string]any:
		if s, ok := v["url"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// flatten unwraps a single value or an array into a slice.
func flatten(v any) []any {
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{v}
}

// priceOf reads price in its several published forms: a number, a numeric
// string, or a nested priceSpecification. The currency is returned because a
// specification may carry it where the offer does not.
func priceOf(m map[string]any) (float64, string, bool) {
	if p, ok := numberOf(m["price"]); ok {
		cur, _ := m["priceCurrency"].(string)
		return p, cur, true
	}
	if spec, present := m["priceSpecification"]; present {
		for _, s := range flatten(spec) {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := numberOf(sm["price"]); ok {
				cur, _ := sm["priceCurrency"].(string)
				return p, cur, true
			}
		}
	}
	return 0, "", false
}

func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		// Prices arrive as "12.99", "$12.99", or "12,99" depending on locale.
		cleaned := strings.Map(func(r rune) rune {
			if (r >= '0' && r <= '9') || r == '.' || r == '-' {
				return r
			}
			return -1
		}, n)
		if cleaned == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(cleaned, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// parseAvailability normalizes a schema.org availability value.
//
// Values arrive as full URLs (https://schema.org/OutOfStock) and, on at least
// one measured host, with literal whitespace inside the URL:
//
//	"http://schema.org/ \n            OutOfStock"
//
// so every run of whitespace is removed before the final path segment is read.
// Anything unrecognized becomes AvailabilityUnknown rather than a guess:
// PreOrder, BackOrder and LimitedAvailability all mean the item can still be
// had, and reporting them as unavailable would hide wanted things.
func parseAvailability(raw string) Availability {
	compact := strings.Join(strings.Fields(raw), "")
	compact = strings.TrimRight(compact, "/")
	if i := strings.LastIndex(compact, "/"); i >= 0 {
		compact = compact[i+1:]
	}

	switch {
	case strings.EqualFold(compact, "OutOfStock"),
		strings.EqualFold(compact, "SoldOut"):
		return AvailabilityOutOfStock
	case strings.EqualFold(compact, "Discontinued"):
		return AvailabilityDiscontinued
	case strings.EqualFold(compact, "InStock"):
		return AvailabilityInStock
	}
	return AvailabilityUnknown
}
