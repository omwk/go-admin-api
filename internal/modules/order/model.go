// Package order persists atomic order facts without registering ingestion or HTTP routes.
package order

import (
	"encoding/json"
	"time"
)

// States contains independent facts; the display status is never stored.
type States struct {
	Lifecycle   string `gorm:"column:lifecycle_status"`
	Fulfillment string `gorm:"column:fulfillment_status"`
	Payment     string `gorm:"column:payment_status"`
	AfterSales  string `gorm:"column:after_sales_status"`
	Cancel      string `gorm:"column:cancel_status"`
}

// Money preserves platform amounts in the currency's smallest unit.
type Money struct {
	GoodsAmountCents    int64
	ShippingFeeCents    int64
	DiscountCents       int64
	TotalAmountCents    int64
	PaidAmountCents     int64
	RefundedAmountCents int64
}

// Order is the root identified by the immutable platform instance, not OrderNo.
type Order struct {
	ID                  int64 `gorm:"primaryKey"`
	TenantID            int64
	ShopID              int64
	PlatformID          int64
	ExternalOrderID     string
	OrderNo             string
	States              `gorm:"embedded"`
	PlatformStatus      string
	Currency            string
	Money               `gorm:"embedded"`
	FormulaDeltaCents   int64
	PaymentDeltaCents   int64
	PaymentSettled      bool
	ItemCount           int
	LastRefundedAt      *time.Time
	RefundRejectionCode string
	Revision            *int64
	Version             int64
	RawID               int64
	SourceFetchedAt     time.Time
	MappingChecksum     string
	ContentHash         string
	PlacedAt            *time.Time
	SyncedAt            time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           *time.Time
	VanishedAt          *time.Time
	VanishReason        *string
}

// TableName implements the singular table convention.
func (Order) TableName() string { return "sales_order" }

// Item retains the original sale snapshot even after catalog changes.
type Item struct {
	ID                  int64 `gorm:"primaryKey"`
	TenantID            int64
	OrderID             int64
	ExternalItemID      string
	ListingID           *int64
	ListingSKUID        *int64 `gorm:"column:listing_sku_id"`
	SKUID               *int64 `gorm:"column:sku_id"`
	Title               string
	SKUTitle            string
	ImageURL            string
	Currency            string
	PriceCents          int64
	Qty                 int64
	DiscountCents       int64
	AmountCents         int64
	OrderDiscountCents  int64
	AllocationSource    string
	RefundedAmountCents *int64
	RefundStatus        string
	LastRefundedAt      *time.Time
	RefundRejectionCode string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           *time.Time
}

// TableName implements the singular table convention.
func (Item) TableName() string { return "order_item" }

// Address contains three authenticated envelopes and no cleartext projection.
type Address struct {
	ID                 int64     `gorm:"primaryKey" json:"-"`
	TenantID           int64     `json:"-"`
	OrderID            int64     `json:"-"`
	ReceiverNameCipher []byte    `json:"-"`
	ReceiverNameNonce  []byte    `json:"-"`
	PhoneCipher        []byte    `json:"-"`
	PhoneNonce         []byte    `json:"-"`
	AddressCipher      []byte    `json:"-"`
	AddressNonce       []byte    `json:"-"`
	KeyVersion         int       `json:"-"`
	Revision           int64     `json:"-"`
	CreatedAt          time.Time `json:"-"`
	UpdatedAt          time.Time `json:"-"`
}

// TableName implements the singular table convention.
func (Address) TableName() string { return "order_address" }

// StatusHistory is append only, including rejected candidates as explicit conflicts.
type StatusHistory struct {
	ID         int64 `gorm:"primaryKey"`
	TenantID   int64
	OrderID    int64
	Dimension  string
	FromStatus string
	ToStatus   string
	EventKind  string
	Reason     string
	Evidence   json.RawMessage `gorm:"type:jsonb"`
	EventKey   string
	OccurredAt *time.Time
	ObservedAt time.Time
}

// TableName implements the singular table convention.
func (StatusHistory) TableName() string { return "order_status_history" }

// SyncPending is storage reserved for M5b; M5a never schedules or consumes it.
type SyncPending struct {
	ID              int64 `gorm:"primaryKey"`
	TenantID        int64
	ShopID          int64
	PlatformID      int64
	ExternalOrderID string
	Revision        *int64
	ListedUpdatedAt *time.Time
	Status          string
	Attempts        int
	NextRetryAt     *time.Time
	ReasonCode      string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TableName implements the singular table convention.
func (SyncPending) TableName() string { return "order_sync_pending" }
