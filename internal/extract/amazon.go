package extract

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// AmazonRaw is the structured evaluation payload returned from the browser
// running against an Amazon product page.
type AmazonRaw struct {
	Title       string `json:"title"`
	Offscreen   string `json:"offscreen"`   // e.g. "$49.99"
	Whole       string `json:"whole"`       // e.g. "49"
	Fraction    string `json:"fraction"`    // e.g. "99"
	Symbol      string `json:"symbol"`      // e.g. "$"
	AvailText   string `json:"availText"`   // e.g. "In Stock", "Currently unavailable."
	ImageURL    string `json:"imageUrl"`    // Product main image URL
	Description string `json:"description"` // Short bullet or description text
	RawHTML     string `json:"rawHtml"`     // Key HTML container or payload snapshot
	IsDogPage   bool   `json:"isDogPage"`   // 404 / Dogs of Amazon
	IsCaptcha   bool   `json:"isCaptcha"`   // Robot check / captcha wall
}

var priceRegex = regexp.MustCompile(`([0-9]+(?:\.[0-9]{2})?)`)

// Amazon parses the raw signals extracted from an Amazon product DOM into a
// standard extract.Result.
func Amazon(raw AmazonRaw) (Result, error) {
	if raw.IsCaptcha {
		return Result{Status: "blocked", Detail: "Amazon robot check / captcha wall"}, nil
	}
	if raw.IsDogPage {
		return Result{Status: "dead", Detail: "Amazon page not found (dog page)"}, nil
	}

	res := Result{
		Source:       "amazon-dom",
		Status:       "no_data",
		Availability: AvailabilityUnknown,
		ImageURL:     strings.TrimSpace(raw.ImageURL),
		Description:  strings.TrimSpace(raw.Description),
		RawPayload:   strings.TrimSpace(raw.RawHTML),
	}

	// 1. Availability
	availLower := strings.ToLower(raw.AvailText)
	switch {
	case strings.Contains(availLower, "currently unavailable"),
		strings.Contains(availLower, "temporarily out of stock"),
		strings.Contains(availLower, "out of stock"):
		res.Availability = AvailabilityOutOfStock
		f := false
		res.Available = &f
	case strings.Contains(availLower, "in stock"),
		strings.Contains(availLower, "left in stock"):
		res.Availability = AvailabilityInStock
		t := true
		res.Available = &t
	}

	// 2. Price
	var parsedPrice *float64
	var currency string

	if raw.Offscreen != "" {
		if strings.Contains(raw.Offscreen, "$") {
			currency = "USD"
		}
		if match := priceRegex.FindString(raw.Offscreen); match != "" {
			if v, err := strconv.ParseFloat(match, 64); err == nil && v > 0 {
				parsedPrice = &v
			}
		}
	}

	if parsedPrice == nil && raw.Whole != "" {
		wholeStr := strings.TrimSpace(raw.Whole)
		fracStr := strings.TrimSpace(raw.Fraction)
		if fracStr == "" {
			fracStr = "00"
		}
		if v, err := strconv.ParseFloat(fmt.Sprintf("%s.%s", wholeStr, fracStr), 64); err == nil && v > 0 {
			parsedPrice = &v
			if raw.Symbol == "$" || currency == "" {
				currency = "USD"
			}
		}
	}

	if parsedPrice != nil {
		res.Price = parsedPrice
		if currency == "" {
			currency = "USD"
		}
		res.Currency = currency
		res.Status = "priced"
	}

	return res, nil
}
