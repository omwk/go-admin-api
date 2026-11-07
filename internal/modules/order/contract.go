package order

import "time"

// ChangeMode separates observed facts, commands and approved corrections.
type ChangeMode string

// Supported modes do not expose any transport endpoints.
const (
	ChangeSnapshot   ChangeMode = "snapshot"
	ChangeCommand    ChangeMode = "command"
	ChangeCorrection ChangeMode = "correction"
)

// Source identifies a protected raw observation and its frozen interpretation.
type Source struct {
	RawID           int64     `json:"raw_id"`
	FetchedAt       time.Time `json:"fetched_at"`
	MappingChecksum string    `json:"mapping_checksum"`
}

// Correction carries approval and upstream evidence, not arbitrary raw payloads.
type Correction struct {
	ApprovedBy                 int64  `json:"approved_by"`
	ReasonCode                 string `json:"reason_code"`
	AuthoritativeDetail        bool   `json:"authoritative_detail"`
	PlatformSupportsCorrection bool   `json:"platform_supports_correction"`
}

// AddressInput is transient sensitive data; it must never be logged or serialized.
type AddressInput struct {
	ReceiverName []byte `json:"-"`
	Phone        []byte `json:"-"`
	Address      []byte `json:"-"`
}

// ItemInput contains immutable sale facts plus cumulative refund summaries.
// AmountCents is after the line discount and before the order-level discount.
type ItemInput struct {
	ExternalItemID      string
	Title               string
	SKUTitle            string
	ImageURL            string
	Currency            string
	PriceCents          int64
	Qty                 int64
	DiscountCents       int64
	AmountCents         int64
	DiscountEligible    bool
	OrderDiscountCents  *int64
	RefundedAmountCents *int64
	RefundStatus        string
	LastRefundedAt      *time.Time
	RefundRejectionCode string
}

// ItemSet explicitly proves completeness; nil Snapshot.Items means omitted.
type ItemSet struct {
	Rows             []ItemInput
	Complete         bool
	HasMore          bool
	ExpectedCount    int
	EmptyConfirmed   bool
	AllocationSource string
}

// Snapshot is a canonical aggregate supplied by a trusted adapter.
// Apply verifies its shop/platform and raw directory relationships again.
type Snapshot struct {
	TenantID            int64
	ShopID              int64
	PlatformID          int64
	ExternalOrderID     string
	StableIdentity      bool
	OrderNo             string
	States              States
	PlatformStatus      string
	Currency            string
	Money               Money
	PaymentSettled      bool
	LastRefundedAt      *time.Time
	RefundRejectionCode string
	PlacedAt            *time.Time
	ExpectedVersion     int64
	Revision            *int64
	Source              Source
	Mode                ChangeMode
	Correction          *Correction
	OccurredAt          *time.Time
	Items               *ItemSet
	Address             *AddressInput `json:"-"`
}

// Result is safe to report and contains no PII or upstream error material.
type Result struct {
	OrderID int64
	Version int64
	Outcome string
	Reason  string
	Deltas  Deltas
}
