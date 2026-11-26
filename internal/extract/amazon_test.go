package extract

import (
	"testing"
)

func TestAmazonExtraction(t *testing.T) {
	tests := []struct {
		name         string
		raw          AmazonRaw
		wantStatus   string
		wantPrice    *float64
		wantCurrency string
		wantAvail    Availability
	}{
		{
			name: "clean in stock with offscreen price",
			raw: AmazonRaw{
				Title:     "HYSHIKRA USB-A Microphone",
				Offscreen: "$49.99",
				Whole:     "49",
				Fraction:  "99",
				Symbol:    "$",
				AvailText: "In Stock",
			},
			wantStatus:   "priced",
			wantPrice:    ptrFloat(49.99),
			wantCurrency: "USD",
			wantAvail:    AvailabilityInStock,
		},
		{
			name: "low stock with whole/fraction fallback",
			raw: AmazonRaw{
				Title:     "Shelly 1 Gen4 UL",
				Offscreen: "",
				Whole:     "54",
				Fraction:  "86",
				Symbol:    "$",
				AvailText: "Only 19 left in stock - order soon.",
			},
			wantStatus:   "priced",
			wantPrice:    ptrFloat(54.86),
			wantCurrency: "USD",
			wantAvail:    AvailabilityInStock,
		},
		{
			name: "currently unavailable without price",
			raw: AmazonRaw{
				Title:     "Vintage Rare Widget",
				AvailText: "Currently unavailable. We don't know when or if this item will be back in stock.",
			},
			wantStatus: "no_data",
			wantAvail:  AvailabilityOutOfStock,
		},
		{
			name: "dog page (404 / dead)",
			raw: AmazonRaw{
				IsDogPage: true,
			},
			wantStatus: "dead",
		},
		{
			name: "captcha / robot check (blocked)",
			raw: AmazonRaw{
				IsCaptcha: true,
			},
			wantStatus: "blocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Amazon(tt.raw)
			if err != nil {
				t.Fatalf("Amazon error: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if tt.wantPrice != nil {
				if got.Price == nil || *got.Price != *tt.wantPrice {
					t.Errorf("price = %v, want %v", got.Price, *tt.wantPrice)
				}
			}
			if tt.wantCurrency != "" && got.Currency != tt.wantCurrency {
				t.Errorf("currency = %q, want %q", got.Currency, tt.wantCurrency)
			}
			if tt.wantAvail != "" && got.Availability != tt.wantAvail {
				t.Errorf("availability = %q, want %q", got.Availability, tt.wantAvail)
			}
		})
	}
}

func ptrFloat(v float64) *float64 {
	return &v
}

func TestAmazonExtractsImageAndDescription(t *testing.T) {
	raw := AmazonRaw{
		Title:       "Test Amazon Product",
		Offscreen:   "$25.00",
		AvailText:   "In Stock",
		ImageURL:    "https://m.media-amazon.com/images/I/sample.jpg",
		Description: "Great features and long battery life.",
		RawHTML:     "<div id='dp'>...</div>",
	}

	res, err := Amazon(raw)
	if err != nil {
		t.Fatalf("Amazon error: %v", err)
	}
	if res.ImageURL != raw.ImageURL {
		t.Errorf("ImageURL = %q, want %q", res.ImageURL, raw.ImageURL)
	}
	if res.Description != raw.Description {
		t.Errorf("Description = %q, want %q", res.Description, raw.Description)
	}
	if res.RawPayload != raw.RawHTML {
		t.Errorf("RawPayload = %q, want %q", res.RawPayload, raw.RawHTML)
	}
}
