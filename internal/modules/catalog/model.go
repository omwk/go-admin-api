package catalog

import (
	"encoding/json"
	"time"
)

// Category is an internally managed product category.
type Category struct {
	ID        int64     `json:"id" gorm:"column:id;primaryKey"`
	TenantID  int64     `json:"tenant_id" gorm:"column:tenant_id;not null"`
	ParentID  int64     `json:"parent_id" gorm:"column:parent_id;not null"`
	Name      string    `json:"name" gorm:"column:name;not null"`
	Path      string    `json:"path" gorm:"column:path;not null"`
	Level     int       `json:"level" gorm:"column:level;not null"`
	Sort      int       `json:"sort" gorm:"column:sort;not null"`
	Status    string    `json:"status" gorm:"column:status;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;not null"`
}

func (Category) TableName() string { return "category" }

// PlatformCategory is the synchronized category projection for one platform.
type PlatformCategory struct {
	ID               int64     `json:"id" gorm:"column:id;primaryKey"`
	TenantID         int64     `json:"tenant_id" gorm:"column:tenant_id;not null"`
	PlatformID       int64     `json:"platform_id" gorm:"column:platform_id;not null"`
	ExternalID       string    `json:"external_id" gorm:"column:external_id;not null"`
	ParentExternalID string    `json:"parent_external_id" gorm:"column:parent_external_id;not null"`
	Name             string    `json:"name" gorm:"column:name;not null"`
	Path             string    `json:"path" gorm:"column:path;not null"`
	IsLeaf           bool      `json:"is_leaf" gorm:"column:is_leaf;not null"`
	SyncedAt         time.Time `json:"synced_at" gorm:"column:synced_at;not null"`
}

func (PlatformCategory) TableName() string { return "platform_category" }

// CategoryMapping stores the applied or unmatched mapping for a platform category.
type CategoryMapping struct {
	ID                 int64     `json:"id" gorm:"column:id;primaryKey"`
	TenantID           int64     `json:"tenant_id" gorm:"column:tenant_id;not null"`
	PlatformID         int64     `json:"platform_id" gorm:"column:platform_id;not null"`
	PlatformCategoryID int64     `json:"platform_category_id" gorm:"column:platform_category_id;not null"`
	CategoryID         *int64    `json:"category_id" gorm:"column:category_id"`
	Confidence         float64   `json:"confidence" gorm:"column:confidence;not null"`
	Source             string    `json:"source" gorm:"column:source;not null"`
	CreatedAt          time.Time `json:"created_at" gorm:"column:created_at;not null"`
	UpdatedAt          time.Time `json:"updated_at" gorm:"column:updated_at;not null"`
}

func (CategoryMapping) TableName() string { return "category_mapping" }

// ProductSPU is a canonical product with denormalized listing summary fields.
type ProductSPU struct {
	ID            int64           `json:"id" gorm:"column:id;primaryKey"`
	TenantID      int64           `json:"tenant_id" gorm:"column:tenant_id;not null"`
	Code          *string         `json:"code" gorm:"column:code"`
	Title         string          `json:"title" gorm:"column:title;not null"`
	Subtitle      string          `json:"subtitle" gorm:"column:subtitle;not null"`
	Brand         string          `json:"brand" gorm:"column:brand;not null"`
	CategoryID    *int64          `json:"category_id" gorm:"column:category_id"`
	MainImage     string          `json:"main_image" gorm:"column:main_image;not null"`
	Description   string          `json:"description" gorm:"column:description;not null"`
	Status        string          `json:"status" gorm:"column:status;not null"`
	Source        string          `json:"source" gorm:"column:source;not null"`
	Attributes    json.RawMessage `json:"attributes" gorm:"column:attributes;type:jsonb;not null"`
	Ext           json.RawMessage `json:"ext" gorm:"column:ext;type:jsonb;not null"`
	PriceMinCents int64           `json:"price_min_cents" gorm:"column:price_min_cents;not null"`
	PriceMaxCents int64           `json:"price_max_cents" gorm:"column:price_max_cents;not null"`
	SKUCount      int             `json:"sku_count" gorm:"column:sku_count;not null"`
	Version       int             `json:"version" gorm:"column:version;not null;default:0"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at;not null"`
	UpdatedAt     time.Time       `json:"updated_at" gorm:"column:updated_at;not null"`
	DeletedAt     *time.Time      `json:"deleted_at,omitempty" gorm:"column:deleted_at"`
}

func (ProductSPU) TableName() string { return "product_spu" }

// ProductSKU is a canonical product variant.
type ProductSKU struct {
	ID          int64           `json:"id" gorm:"column:id;primaryKey"`
	TenantID    int64           `json:"tenant_id" gorm:"column:tenant_id;not null"`
	SPUID       int64           `json:"spu_id" gorm:"column:spu_id;not null"`
	Code        *string         `json:"code" gorm:"column:code"`
	Barcode     string          `json:"barcode" gorm:"column:barcode;not null"`
	Title       string          `json:"title" gorm:"column:title;not null"`
	PriceCents  int64           `json:"price_cents" gorm:"column:price_cents;not null"`
	CostCents   int64           `json:"cost_cents" gorm:"column:cost_cents;not null"`
	Currency    string          `json:"currency" gorm:"column:currency;type:char(3);not null"`
	StockCached int             `json:"stock_cached" gorm:"column:stock_cached;not null"`
	Attributes  json.RawMessage `json:"attributes" gorm:"column:attributes;type:jsonb;not null"`
	Status      string          `json:"status" gorm:"column:status;not null"`
	CreatedAt   time.Time       `json:"created_at" gorm:"column:created_at;not null"`
	UpdatedAt   time.Time       `json:"updated_at" gorm:"column:updated_at;not null"`
	DeletedAt   *time.Time      `json:"deleted_at,omitempty" gorm:"column:deleted_at"`
}

func (ProductSKU) TableName() string { return "product_sku" }

// ProductMedia stores ordered product or SKU media references.
type ProductMedia struct {
	ID        int64     `json:"id" gorm:"column:id;primaryKey"`
	TenantID  int64     `json:"tenant_id" gorm:"column:tenant_id;not null"`
	SPUID     int64     `json:"spu_id" gorm:"column:spu_id;not null"`
	SKUID     *int64    `json:"sku_id" gorm:"column:sku_id"`
	Type      string    `json:"type" gorm:"column:type;not null"`
	URL       string    `json:"url" gorm:"column:url;not null"`
	Sort      int       `json:"sort" gorm:"column:sort;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;not null"`
}

func (ProductMedia) TableName() string { return "product_media" }

// InventorySnapshot records one observed listing SKU quantity set.
type InventorySnapshot struct {
	ID           int64     `json:"id" gorm:"column:id;primaryKey"`
	TenantID     int64     `json:"tenant_id" gorm:"column:tenant_id;not null"`
	ShopID       int64     `json:"shop_id" gorm:"column:shop_id;not null"`
	ListingSKUID int64     `json:"listing_sku_id" gorm:"column:listing_sku_id;not null"`
	Available    int       `json:"available" gorm:"column:available;not null"`
	Reserved     int       `json:"reserved" gorm:"column:reserved;not null"`
	SyncedAt     time.Time `json:"synced_at" gorm:"column:synced_at;not null"`
}

func (InventorySnapshot) TableName() string { return "inventory_snapshot" }

// ExportJob persists an asynchronous export request; execution belongs to M4b.
type ExportJob struct {
	ID          int64           `json:"id" gorm:"column:id;primaryKey"`
	TenantID    int64           `json:"tenant_id" gorm:"column:tenant_id;not null"`
	Kind        string          `json:"kind" gorm:"column:kind;not null"`
	Status      string          `json:"status" gorm:"column:status;not null"`
	Filters     json.RawMessage `json:"filters" gorm:"column:filters;type:jsonb;not null"`
	RequestedBy int64           `json:"requested_by" gorm:"column:requested_by;not null"`
	FileID      *int64          `json:"file_id" gorm:"column:file_id"`
	RowCount    int64           `json:"row_count" gorm:"column:row_count;not null"`
	ErrorMsg    string          `json:"error_msg" gorm:"column:error_msg;not null"`
	ObjectKey   string          `json:"object_key" gorm:"column:object_key;not null"`
	ExpiresAt   *time.Time      `json:"expires_at" gorm:"column:expires_at"`
	CreatedAt   time.Time       `json:"created_at" gorm:"column:created_at;not null"`
	StartedAt   *time.Time      `json:"started_at" gorm:"column:started_at"`
	FinishedAt  *time.Time      `json:"finished_at" gorm:"column:finished_at"`
}

func (ExportJob) TableName() string { return "export_job" }
