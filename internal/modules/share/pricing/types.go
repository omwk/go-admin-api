// Package pricing resolves tenant/customer prices from explicit, clock-free inputs.
package pricing

import "time"

// Basis identifies the winning source layer independently from SKU specificity.
type Basis string

// Price layers are evaluated in this order.
const (
	CustomerQuote Basis = "customer_quote"
	CustomerList  Basis = "customer_price_list"
	DefaultList   Basis = "default_price_list"
	Original      Basis = "original"
	Unpriced      Basis = "unpriced"
)

// Candidate is one authenticated, versioned price-list item with parent metadata.
type Candidate struct {
	TenantID, CustomerID, PriceListID, ItemID int64
	SPUID                                     int64 `gorm:"column:spu_id"`
	SKUID                                     int64 `gorm:"column:sku_id"`
	Version                                   int
	Type, Status, Currency                    string
	PriceCents                                int64
	MOQ                                       int
	ValidFrom, ValidTo                        *time.Time
}

// Input supplies the exact identity, policy and observation time for resolution.
type Input struct {
	TenantID, CustomerID, PriceListID, SPUID, SKUID int64
	CustomerActive                                  bool
	Currency                                        string
	AsOf                                            time.Time
	Quantity                                        int
	AllowFallback, AllowZero                        bool
	OriginalPriceCents                              *int64
	Candidates                                      []Candidate
}

// Result carries safe price terms plus internal evidence for management previews.
type Result struct {
	PriceCents  *int64    `json:"price_cents"`
	Currency    string    `json:"currency"`
	ShowPrice   bool      `json:"show_price"`
	PriceBasis  Basis     `json:"price_basis"`
	LayerName   string    `json:"layer_name"`
	AsOf        time.Time `json:"as_of"`
	PriceListID int64     `json:"price_list_id,omitempty"`
	ItemID      int64     `json:"item_id,omitempty"`
	Version     int       `json:"version,omitempty"`
	Precision   string    `json:"precision,omitempty"`
	MOQ         int       `json:"moq"`
	Reason      string    `json:"reason"`
}
