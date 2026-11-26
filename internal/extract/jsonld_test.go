package extract

import "testing"

func page(ld string) string {
	return `<!doctype html><html><head><title>x</title>
<script type="application/ld+json">` + ld + `</script>
</head><body><p>hello</p></body></html>`
}

func TestJSONLD(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		wantStat  string
		wantPrice *float64
		wantCur   string
	}{
		{
			name: "simple product",
			doc: page(`{"@type":"Product","name":"Widget",
				"offers":{"@type":"Offer","price":"19.99","priceCurrency":"usd","availability":"https://schema.org/InStock"}}`),
			wantStat:  "priced",
			wantPrice: ptr(19.99),
			wantCur:   "USD",
		},
		{
			name: "product nested in @graph",
			doc: page(`{"@context":"https://schema.org","@graph":[
				{"@type":"WebSite","name":"Shop"},
				{"@type":"Product","name":"Widget","offers":{"price":42,"priceCurrency":"EUR"}}]}`),
			wantStat:  "priced",
			wantPrice: ptr(42.0),
			wantCur:   "EUR",
		},
		{
			name: "offers as an array picks the first priced one",
			doc: page(`{"@type":"Product","offers":[
				{"@type":"Offer","availability":"https://schema.org/OutOfStock"},
				{"@type":"Offer","price":"7.50","priceCurrency":"GBP"}]}`),
			wantStat:  "priced",
			wantPrice: ptr(7.5),
			wantCur:   "GBP",
		},
		{
			name: "price nested in priceSpecification",
			doc: page(`{"@type":"Product","offers":{"priceSpecification":{
				"@type":"UnitPriceSpecification","price":"123.45","priceCurrency":"USD"}}}`),
			wantStat:  "priced",
			wantPrice: ptr(123.45),
			wantCur:   "USD",
		},
		{
			name:      "currency symbol in the price string",
			doc:       page(`{"@type":"Product","offers":{"price":"$1,299.00","priceCurrency":"USD"}}`),
			wantStat:  "priced",
			wantPrice: ptr(1299.0),
			wantCur:   "USD",
		},
		{
			name:     "no ld+json at all",
			doc:      `<html><body>plain</body></html>`,
			wantStat: "no_data",
		},
		{
			name:     "ld+json present but not a product",
			doc:      page(`{"@type":"WebSite","name":"Shop"}`),
			wantStat: "no_data",
		},
		{
			name:     "product with no offers",
			doc:      page(`{"@type":"Product","name":"Widget"}`),
			wantStat: "no_data",
		},
		{
			name:     "malformed json is skipped, not fatal",
			doc:      page(`{not json at all`),
			wantStat: "no_data",
		},
		{
			name: "malformed block falls through to a later good one",
			doc: `<html><head>
				<script type="application/ld+json">{broken</script>
				<script type="application/ld+json">{"@type":"Product","offers":{"price":"5","priceCurrency":"USD"}}</script>
				</head></html>`,
			wantStat:  "priced",
			wantPrice: ptr(5.0),
			wantCur:   "USD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := JSONLD(tt.doc)
			if err != nil {
				t.Fatalf("JSONLD: %v", err)
			}
			if got.Status != tt.wantStat {
				t.Fatalf("status = %q, want %q (detail %q)", got.Status, tt.wantStat, got.Detail)
			}
			if tt.wantPrice == nil {
				if got.Price != nil {
					t.Fatalf("price = %v, want nil", *got.Price)
				}
				return
			}
			if got.Price == nil {
				t.Fatalf("price = nil, want %v", *tt.wantPrice)
			}
			if *got.Price != *tt.wantPrice {
				t.Errorf("price = %v, want %v", *got.Price, *tt.wantPrice)
			}
			if got.Currency != tt.wantCur {
				t.Errorf("currency = %q, want %q", got.Currency, tt.wantCur)
			}
		})
	}
}

func TestJSONLDEmptyDocument(t *testing.T) {
	if _, err := JSONLD("   "); err == nil {
		t.Fatal("expected an error for an empty document")
	}
}

func TestJSONLDSourceIsSet(t *testing.T) {
	got, err := JSONLD(page(`{"@type":"Product","offers":{"price":"1","priceCurrency":"USD"}}`))
	if err != nil {
		t.Fatalf("JSONLD: %v", err)
	}
	if got.Source != "jsonld" {
		t.Fatalf("source = %q, want jsonld", got.Source)
	}
}

func ptr[T any](v T) *T { return &v }

func TestParseAvailability(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Availability
	}{
		{"plain InStock", "InStock", AvailabilityInStock},
		{"schema.org URL", "https://schema.org/InStock", AvailabilityInStock},
		{"schema.org URL out of stock", "https://schema.org/OutOfStock", AvailabilityOutOfStock},
		{"SoldOut treated as out of stock", "https://schema.org/SoldOut", AvailabilityOutOfStock},
		{"discontinued", "https://schema.org/Discontinued", AvailabilityDiscontinued},
		// Measured on shop.startrek.com: a literal newline inside the URL.
		{"whitespace inside the URL", "http://schema.org/ \n            OutOfStock", AvailabilityOutOfStock},
		{"trailing slash", "https://schema.org/OutOfStock/", AvailabilityOutOfStock},
		// These all mean the item can still be had; treating them as
		// unavailable would hide wanted things.
		{"PreOrder is not unavailable", "https://schema.org/PreOrder", AvailabilityUnknown},
		{"BackOrder is not unavailable", "https://schema.org/BackOrder", AvailabilityUnknown},
		{"LimitedAvailability is not unavailable", "https://schema.org/LimitedAvailability", AvailabilityUnknown},
		{"empty", "", AvailabilityUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAvailability(tt.raw); got != tt.want {
				t.Fatalf("parseAvailability(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// The measurement that shaped this: every page whose structured data says
// InStock also contains the literal text "sold out" or "out of stock" in
// recommendation widgets. Text must never override the structured value.
func TestOutOfStockTextDoesNotOverrideInStockData(t *testing.T) {
	doc := page(`{"@type":"Product","offers":{"price":"25.00","priceCurrency":"USD",
		"availability":"https://schema.org/InStock"}}`)
	// The kind of boilerplate that fooled a naive text matcher.
	doc = doc[:len(doc)-len("</body></html>")] +
		`<div class="recommendations">Sold out! Out of stock! No longer available</div>` +
		`</body></html>`

	got, err := JSONLD(doc)
	if err != nil {
		t.Fatalf("JSONLD: %v", err)
	}
	if got.Availability != AvailabilityInStock {
		t.Fatalf("availability = %q, want InStock despite the page text", got.Availability)
	}
	if got.Availability.Unavailable() {
		t.Fatal("an in-stock item was marked unavailable because of boilerplate text")
	}
}

func TestOutOfStockWithPriceStillReportsUnavailable(t *testing.T) {
	// A sold-out item frequently still shows its last price. The price is real
	// and worth recording; the item just must not sit on the live list.
	doc := page(`{"@type":"Product","offers":{"price":"40.00","priceCurrency":"USD",
		"availability":"https://schema.org/OutOfStock"}}`)

	got, err := JSONLD(doc)
	if err != nil {
		t.Fatalf("JSONLD: %v", err)
	}
	if !got.Availability.Unavailable() {
		t.Fatalf("availability = %q, want unavailable", got.Availability)
	}
	if got.Price == nil || *got.Price != 40.0 {
		t.Fatal("the last known price should still be captured")
	}
}

func TestJSONLDExtractsImageAndDescription(t *testing.T) {
	doc := page(`{
		"@type": "Product",
		"name": "Super Coffee Grinder",
		"description": "Conical burr grinder with 40 settings.",
		"image": "https://example.com/grinder.jpg",
		"offers": {
			"price": "99.00",
			"priceCurrency": "USD",
			"availability": "https://schema.org/InStock"
		}
	}`)

	got, err := JSONLD(doc)
	if err != nil {
		t.Fatalf("JSONLD: %v", err)
	}
	if got.ImageURL != "https://example.com/grinder.jpg" {
		t.Errorf("ImageURL = %q, want 'https://example.com/grinder.jpg'", got.ImageURL)
	}
	if got.Description != "Conical burr grinder with 40 settings." {
		t.Errorf("Description = %q, want 'Conical burr grinder with 40 settings.'", got.Description)
	}
	if got.RawPayload == "" {
		t.Error("expected RawPayload to retain the raw json block")
	}
}

func TestJSONLDExtractsImageFromArrayAndObject(t *testing.T) {
	doc1 := page(`{
		"@type": "Product",
		"image": ["https://example.com/first.jpg", "https://example.com/second.jpg"],
		"offers": {"price": "10.00", "priceCurrency": "USD"}
	}`)
	got1, err := JSONLD(doc1)
	if err != nil || got1.ImageURL != "https://example.com/first.jpg" {
		t.Errorf("got ImageURL = %q, want first.jpg", got1.ImageURL)
	}

	doc2 := page(`{
		"@type": "Product",
		"image": {"@type": "ImageObject", "url": "https://example.com/obj.jpg"},
		"offers": {"price": "10.00", "priceCurrency": "USD"}
	}`)
	got2, err := JSONLD(doc2)
	if err != nil || got2.ImageURL != "https://example.com/obj.jpg" {
		t.Errorf("got ImageURL = %q, want obj.jpg", got2.ImageURL)
	}
}
