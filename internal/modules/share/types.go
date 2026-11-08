package share

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/share/pricing"
)

// Actor is an authenticated management identity.
type Actor struct{ TenantID, UserID int64 }

// CustomerInput is a full customer replacement; Version is required on updates.
type CustomerInput struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	ContactName string `json:"contact_name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	Status      string `json:"status"`
	OwnerUserID int64  `json:"owner_user_id"`
	Source      string `json:"source"`
	Version     int    `json:"version"`
}

// ListInput defines price-list metadata and explicit type/ownership.
type ListInput struct {
	Type       string     `json:"type"`
	CustomerID *int64     `json:"customer_id"`
	Name       string     `json:"name"`
	Currency   string     `json:"currency"`
	Status     string     `json:"status"`
	ValidFrom  *time.Time `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to"`
	Note       string     `json:"note"`
	Version    int        `json:"version"`
}

// ItemInput sets or deletes a price for an exact SPU/SKU within one list.
type ItemInput struct {
	SPUID      int64  `json:"spu_id"`
	SKUID      int64  `json:"sku_id"`
	PriceCents int64  `json:"price_cents"`
	MOQ        int    `json:"moq"`
	Note       string `json:"note"`
	Delete     bool   `json:"delete"`
}

// BatchInput applies one validated item batch with an optimistic parent version.
type BatchInput struct {
	Version int         `json:"version"`
	Items   []ItemInput `json:"items"`
}

// BatchResult reports the committed batch and resulting list version.
type BatchResult struct {
	Success int            `json:"success"`
	Failed  []BatchFailure `json:"failed"`
	Version int            `json:"version"`
}

// BatchFailure identifies a failed row without including private request fields.
type BatchFailure struct {
	ID   int64 `json:"id"`
	Code int   `json:"code"`
}

// Query controls bounded list pages and safe exact filters.
type Query struct {
	Page       int    `form:"page"`
	PageSize   int    `form:"page_size"`
	Sort       string `form:"sort"`
	Keyword    string `form:"keyword"`
	Status     string `form:"status"`
	CustomerID int64  `form:"customer_id"`
	Type       string `form:"type"`
}

// Page uses the shared management pagination response contract.
type Page[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// PreviewInput selects an optional exclusive quote and exact product members.
type PreviewInput struct {
	CustomerID    int64         `json:"customer_id"`
	PriceListID   int64         `json:"price_list_id"`
	Currency      string        `json:"currency"`
	AsOf          *time.Time    `json:"as_of"`
	AllowFallback bool          `json:"allow_fallback_price"`
	AllowZero     bool          `json:"allow_zero_price"`
	Items         []PreviewItem `json:"items"`
}

// PreviewItem is one product reference plus the intended purchase quantity.
type PreviewItem struct {
	SPUID    int64 `json:"spu_id"`
	SKUID    int64 `json:"sku_id"`
	Quantity int   `json:"quantity"`
}

// PreviewResult contains only the requested product's resolved price evidence.
type PreviewResult struct {
	SPUID int64 `json:"spu_id"`
	SKUID int64 `json:"sku_id"`
	pricing.Result
}

// ProductPrice is a validated product identity with its same-currency SKU price.
type ProductPrice struct {
	SPUID      int64 `gorm:"column:spu_id"`
	SKUID      int64 `gorm:"column:sku_id"`
	PriceCents *int64
	Currency   string
}

// Store is the transaction-bound persistence and live authorization port.
type Store interface {
	Authorize(context.Context, Actor, string) (context.Context, error)
	Transaction(context.Context, func(context.Context) error) error
	Snapshot(context.Context, func(context.Context) error) error
	NextID(context.Context, string) (int64, error)
	ValidateOwner(context.Context, int64, int64) error
	Customers(context.Context, int64, Query) ([]Customer, int64, error)
	Customer(context.Context, int64, int64, bool) (Customer, error)
	SaveCustomer(context.Context, Customer, bool) error
	DeleteCustomer(context.Context, int64, int64, int) error
	PriceLists(context.Context, int64, Query) ([]PriceList, int64, error)
	PriceList(context.Context, int64, int64, bool) (PriceList, error)
	SavePriceList(context.Context, PriceList, bool) error
	DeletePriceList(context.Context, int64, int64, int) error
	Items(context.Context, int64, int64) ([]Item, error)
	SaveItem(context.Context, Item, bool) error
	DeleteItem(context.Context, int64, int64, ItemInput) error
	BumpList(context.Context, int64, int64, int) error
	Product(context.Context, int64, int64, int64) (ProductPrice, error)
	Candidates(context.Context, int64, int64, []int64) ([]pricing.Candidate, error)
	Audit(context.Context, audit.Event) error
	ReplayVisible(context.Context, int64, string, int64, bool) error
}

// Dependencies contains only the existing kernel ports used by this module.
type Dependencies struct {
	Store       Store
	Idempotency idem.Store
	Sealer      FieldSealer
	Now         func() time.Time
}

// FieldSealer writes a fresh versioned envelope for each private field.
type FieldSealer interface {
	Seal([]byte, string) ([]byte, []byte, int, error)
}
