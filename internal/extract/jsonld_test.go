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
