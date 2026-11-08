package share

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/share/pricing"
)

// ErrTokenCollision requests a new random secret after a unique-index collision.
var ErrTokenCollision = errors.New("share secret collision")

// Link is private persistence state, never an anonymous response.
type Link struct {
	ID, TenantID            int64
	TokenHash, TokenPrefix  string
	Type, Mode, Title       string
	PriceListID, CustomerID *int64
	PasswordHash            *string
	ExpiresAt               *time.Time
	MaxViews, ViewCount     int
	AllowInquiry            bool
	Status                  string
	ContentVersion          int
	CreatedBy               int64
	CreatedAt               time.Time
	RevokedAt               *time.Time
	FrozenVersion           string `gorm:"-"`
}

// TableName identifies the existing share-link table.
func (Link) TableName() string { return "share_link" }

// Member selects an exact SPU and optional SKU subset; an omitted subset freezes all current SKUs.
type Member struct {
	SPUID  int64   `json:"spu_id"`
	SKUIDs []int64 `json:"sku_ids"`
}

// LinkInput contains approved public terms and publication policy.
type LinkInput struct {
	Title         string     `json:"title"`
	CustomerID    int64      `json:"customer_id"`
	PriceListID   int64      `json:"price_list_id"`
	Currency      string     `json:"currency"`
	Mode          string     `json:"mode"`
	Items         []Member   `json:"items"`
	Password      string     `json:"password"`
	ExpiresAt     *time.Time `json:"expires_at"`
	MaxViews      int        `json:"max_views"`
	AllowFallback bool       `json:"allow_fallback_price"`
	AllowZero     bool       `json:"allow_zero_price"`
	RevokeOld     *bool      `json:"revoke_old"`
	AllowInquiry  *bool      `json:"allow_inquiry,omitempty"`
}

// LinkReceipt persists only a safe delivery receipt; URL exists only on the first response.
type LinkReceipt struct {
	ID              int64  `json:"id"`
	Status          string `json:"status"`
	SecretDelivered bool   `json:"secret_delivered"`
	URL             string `json:"url,omitempty"`
}

// LinkView is the secret-free management listing projection.
type LinkView struct {
	ID              int64      `json:"id"`
	Title           string     `json:"title"`
	TokenPrefix     string     `json:"token_prefix"`
	Mode            string     `json:"mode"`
	Status          string     `json:"status"`
	MaxViews        int        `json:"max_views"`
	ViewCount       int        `json:"view_count"`
	ExpiresAt       *time.Time `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	RequirePassword bool       `json:"require_password"`
}

// PublicItem is the approved anonymous product projection.
type PublicItem struct {
	SPUID             int64  `json:"spu_id" gorm:"column:spu_id"`
	SKUID             int64  `json:"sku_id" gorm:"column:sku_id"`
	Title             string `json:"title"`
	SKUTitle          string `json:"sku_title"`
	DisplayPriceCents *int64 `json:"display_price_cents"`
	ShowPrice         bool   `json:"show_price"`
	MOQ               int    `json:"moq"`
	StockDisplay      string `json:"stock_display"`
}

// PublicContent is shared by the root and selected-SPU detail responses.
type PublicContent struct {
	SchemaVersion   int          `json:"schema_version"`
	GeneratedAt     time.Time    `json:"generated_at"`
	Mode            string       `json:"mode"`
	PriceBasis      string       `json:"price_basis"`
	AsOf            time.Time    `json:"as_of"`
	ContentVersion  string       `json:"content_version"`
	Title           string       `json:"title"`
	ValidUntil      *time.Time   `json:"valid_until"`
	Currency        string       `json:"currency"`
	RequirePassword bool         `json:"require_password"`
	AllowInquiry    bool         `json:"allow_inquiry"`
	Items           []PublicItem `json:"items"`
}

// Frozen combines separately typed public content with never-public pricing evidence.
type Frozen struct {
	Content  PublicContent    `json:"content"`
	Evidence []pricing.Result `json:"evidence"`
	Policy   LinkInput        `json:"policy"`
}

// PublicationProduct is the bounded, tenant-validated product input to publication.
type PublicationProduct struct {
	PublicItem
	OriginalPrice int64
	Currency      string
}

// LinkStore adds M6b operations to the existing pricing/authorization port.
type LinkStore interface {
	Store
	Consistent(context.Context, func(context.Context) error) error
	PublicationProducts(context.Context, int64, []Member) ([]PublicationProduct, error)
	Link(context.Context, int64, int64, bool) (Link, error)
	Links(context.Context, int64, Query) ([]Link, int64, error)
	InsertLink(context.Context, *Link, Frozen) error
	RevokeLink(context.Context, int64, int64, time.Time) error
	ResolveLink(context.Context, int64, string) (Link, error)
	Frozen(context.Context, int64, int64) (Frozen, error)
	Admit(context.Context, Link, bool, time.Time) error
	RecordVisit(context.Context, Link, string, time.Time) error
}

// AccessCache provides bounded rate budgets and version-scoped public content only.
type AccessCache interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string, time.Duration) error
	TokenBucket(context.Context, string, float64, int) (bool, int, time.Duration, error)
}

// LinkDependencies uses the existing encrypted-field keyring and cache adapter.
type LinkDependencies struct {
	Store       LinkStore
	Idempotency idem.Store
	Sealer      crypto.Sealer
	Cache       AccessCache
	Now         func() time.Time
}

func publicJSON(content PublicContent) (string, error) {
	raw, err := json.Marshal(content)
	return string(raw), err
}
