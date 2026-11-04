package catalog

import (
	"context"
	"encoding/json"
	"time"
)

// ProductSKUInput is a manual SKU definition accepted by SPU create and update.
type ProductSKUInput struct {
	Code       *string         `json:"code,omitempty"`
	Barcode    string          `json:"barcode"`
	Title      string          `json:"title"`
	PriceCents int64           `json:"price_cents"`
	CostCents  int64           `json:"cost_cents"`
	Currency   string          `json:"currency"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
	Status     string          `json:"status,omitempty"`
}

// ProductCreateRequest creates a manual SPU and its optional complete SKU set.
type ProductCreateRequest struct {
	Code        *string           `json:"code,omitempty"`
	Title       string            `json:"title"`
	Subtitle    string            `json:"subtitle,omitempty"`
	Brand       string            `json:"brand,omitempty"`
	CategoryID  *int64            `json:"category_id,omitempty"`
	MainImage   string            `json:"main_image,omitempty"`
	Description string            `json:"description,omitempty"`
	Status      string            `json:"status,omitempty"`
	Attributes  json.RawMessage   `json:"attributes,omitempty"`
	Ext         json.RawMessage   `json:"ext,omitempty"`
	SKUs        []ProductSKUInput `json:"skus,omitempty"`
}

// ProductUpdateRequest replaces editable manual SPU fields and optional SKU set.
type ProductUpdateRequest struct {
	Version     int                `json:"version"`
	Title       string             `json:"title"`
	Subtitle    string             `json:"subtitle,omitempty"`
	Brand       string             `json:"brand,omitempty"`
	CategoryID  *int64             `json:"category_id,omitempty"`
	MainImage   string             `json:"main_image,omitempty"`
	Description string             `json:"description,omitempty"`
	Status      string             `json:"status,omitempty"`
	Attributes  json.RawMessage    `json:"attributes,omitempty"`
	Ext         json.RawMessage    `json:"ext,omitempty"`
	SKUs        *[]ProductSKUInput `json:"skus,omitempty"`
}

// ProductFilter contains the supported product and listing search filters.
type ProductFilter struct {
	Keyword       string     `json:"keyword,omitempty"`
	ShopID        int64      `json:"shop_id,omitempty"`
	PlatformID    int64      `json:"platform_id,omitempty"`
	Status        string     `json:"status,omitempty"`
	CategoryID    int64      `json:"category_id,omitempty"`
	PriceMinCents *int64     `json:"price_min_cents,omitempty"`
	PriceMaxCents *int64     `json:"price_max_cents,omitempty"`
	UpdatedFrom   *time.Time `json:"updated_from,omitempty"`
	UpdatedTo     *time.Time `json:"updated_to,omitempty"`
	HasPrice      *bool      `json:"has_price,omitempty"`
	IsManual      *bool      `json:"is_manual,omitempty"`
}

// ProductListQuery is a validated offset or signed cursor request.
type ProductListQuery struct {
	ProductFilter
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Sort     string `json:"sort"`
	Cursor   string `json:"cursor,omitempty"`
}

// ProductSummary is the lightweight listing projection; it contains no raw payload.
type ProductSummary struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	MainImage     string    `json:"main_image"`
	PriceMinCents int64     `json:"price_min_cents"`
	PriceMaxCents int64     `json:"price_max_cents"`
	SKUCount      int       `json:"sku_count"`
	ShopNames     []string  `json:"shop_names"`
	Status        string    `json:"status"`
	Source        string    `json:"source"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ProductSKUView deliberately omits internal cost and inventory fields.
type ProductSKUView struct {
	ID         int64           `json:"id"`
	SPUID      int64           `json:"spu_id"`
	Code       *string         `json:"code,omitempty"`
	Barcode    string          `json:"barcode"`
	Title      string          `json:"title"`
	PriceCents int64           `json:"price_cents"`
	Currency   string          `json:"currency"`
	Attributes json.RawMessage `json:"attributes"`
	Status     string          `json:"status"`
}

// ProductView omits extension and ingestion-owned raw metadata from SPU responses.
type ProductView struct {
	ID            int64           `json:"id"`
	TenantID      int64           `json:"tenant_id"`
	Code          *string         `json:"code,omitempty"`
	Title         string          `json:"title"`
	Subtitle      string          `json:"subtitle"`
	Brand         string          `json:"brand"`
	CategoryID    *int64          `json:"category_id"`
	MainImage     string          `json:"main_image"`
	Description   string          `json:"description"`
	Status        string          `json:"status"`
	Source        string          `json:"source"`
	Attributes    json.RawMessage `json:"attributes"`
	PriceMinCents int64           `json:"price_min_cents"`
	PriceMaxCents int64           `json:"price_max_cents"`
	SKUCount      int             `json:"sku_count"`
	Version       int             `json:"version"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

// ListingView is a safe platform projection with reserved ingestion metadata removed.
type ListingView struct {
	ID                int64           `json:"id"`
	ShopID            int64           `json:"shop_id"`
	PlatformID        int64           `json:"platform_id"`
	SPUID             *int64          `json:"spu_id"`
	ExternalProductID string          `json:"external_product_id"`
	Title             string          `json:"title"`
	PriceCents        int64           `json:"price_cents"`
	Currency          string          `json:"currency"`
	Status            string          `json:"status"`
	StockTotal        int             `json:"stock_total"`
	SyncedAt          time.Time       `json:"synced_at"`
	Ext               json.RawMessage `json:"ext"`
}

// ListingSKUView is the safe platform SKU mapping projection.
type ListingSKUView struct {
	ID            int64           `json:"id"`
	SKUID         *int64          `json:"sku_id"`
	ExternalSKUID string          `json:"external_sku_id"`
	Title         string          `json:"title"`
	PriceCents    int64           `json:"price_cents"`
	Stock         int             `json:"stock"`
	Ext           json.RawMessage `json:"ext"`
	SyncedAt      time.Time       `json:"synced_at"`
}

// ProductListingDetail combines a safe listing projection and its platform SKU mappings.
type ProductListingDetail struct {
	ListingView
	SKUs []ListingSKUView `json:"skus"`
}

// ProductDetail is the safe SPU response with bound listings and SKUs.
type ProductDetail struct {
	Product  ProductView            `json:"product"`
	SKUs     []ProductSKUView       `json:"skus"`
	Listings []ProductListingDetail `json:"listings"`
	Media    []ProductMedia         `json:"media"`
}

// ProductPage keeps the established pagination envelope and adds a signed continuation.
type ProductPage struct {
	List       []ProductSummary `json:"list"`
	Total      int64            `json:"total"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// ProductStore is the tenant-scoped persistence port used by ProductService.
type ProductStore interface {
	Transaction(context.Context, func(context.Context) error) error
	ListProducts(context.Context, int64, ProductListQuery, *ProductCursorPosition) ([]ProductSummary, int64, bool, error)
	Product(context.Context, int64, int64) (ProductDetail, error)
	ListListings(context.Context, int64, ProductListQuery) ([]ListingView, int64, error)
	CreateSPU(context.Context, *ProductSPU) error
	UpdateSPU(context.Context, int64, int64, int, ProductUpdateRequest) error
	ArchiveSPU(context.Context, int64, int64, int) error
	ReplaceManualSKUs(context.Context, int64, int64, []ProductSKUInput) error
	MergeListings(context.Context, int64, int64, []int64) error
	UnmergeListing(context.Context, int64, int64, int64) error
	RecomputeSPUSummary(context.Context, int64, int64) error
}

// ProductAuthorizer attaches the verified tenant and data-scope context.
type ProductAuthorizer func(context.Context, int64, int64) (context.Context, error)
