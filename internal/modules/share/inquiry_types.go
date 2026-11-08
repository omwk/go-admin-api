package share

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// Inquiry stores only independently sealed submitted fields and safe workflow data.
type Inquiry struct {
	ID, TenantID, ShareLinkID int64
	CustomerID                *int64
	OwnerUserID               int64
	ContactName               Envelope `gorm:"embedded;embeddedPrefix:contact_name_"`
	ContactPhone              Envelope `gorm:"embedded;embeddedPrefix:contact_phone_"`
	ContactEmail              Envelope `gorm:"embedded;embeddedPrefix:contact_email_"`
	Company                   Envelope `gorm:"embedded;embeddedPrefix:company_"`
	Message                   Envelope `gorm:"embedded;embeddedPrefix:message_"`
	Items                     Envelope `gorm:"embedded;embeddedPrefix:items_"`
	KeyVersion, Revision      int
	Status                    string
	CreatedAt, UpdatedAt      time.Time
	ConvertedAt               *time.Time
	PIIPurgedAt               *time.Time `gorm:"column:pii_purged_at"`
}

// TableName identifies the encrypted inquiry table.
func (Inquiry) TableName() string { return "inquiry" }

// InquiryItem is an exact frozen member plus bounded quantity and private note.
type InquiryItem struct {
	SPUID int64  `json:"spu_id"`
	SKUID int64  `json:"sku_id"`
	Qty   int    `json:"qty"`
	Note  string `json:"note"`
}

// InquiryInput contains self-reported contact information, never identity or ownership.
type InquiryInput struct {
	ContactName  string        `json:"contact_name"`
	ContactPhone string        `json:"contact_phone"`
	ContactEmail string        `json:"contact_email"`
	Company      string        `json:"company"`
	Message      string        `json:"message"`
	Items        []InquiryItem `json:"items"`
	Website      string        `json:"website,omitempty"`
}

// InquiryView deliberately masks all contact and free-text fields.
type InquiryView struct {
	ID           int64      `json:"id"`
	ShareLinkID  int64      `json:"share_link_id"`
	CustomerID   *int64     `json:"customer_id"`
	OwnerUserID  int64      `json:"owner_user_id"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ConvertedAt  *time.Time `json:"converted_at"`
	PIIPurgedAt  *time.Time `json:"pii_purged_at"`
	ContactName  string     `json:"contact_name"`
	ContactPhone string     `json:"contact_phone"`
	ContactEmail string     `json:"contact_email"`
	Company      string     `json:"company"`
	Message      string     `json:"message"`
}

// InquiryReceipt binds a safe submission receipt to one anonymous authorization.
type InquiryReceipt struct {
	TenantID, ShareLinkID, InquiryID int64
	ScopeHash, KeyHash, RequestHash  string
	ExpiresAt                        time.Time
}

// TableName identifies the independent anonymous idempotency namespace.
func (InquiryReceipt) TableName() string { return "share_inquiry_receipt" }

// Visit is a minimal event; there is no visitor identity, address or user-agent.
type Visit struct {
	ID          int64     `json:"id"`
	ShareLinkID int64     `json:"share_link_id"`
	Result      string    `json:"result"`
	CreatedAt   time.Time `json:"created_at"`
}

// InquiryStore owns anonymous writes and live human-scoped inquiry/visit queries.
type InquiryStore interface {
	Authorize(context.Context, Actor, string) (context.Context, error)
	Transaction(context.Context, func(context.Context) error) error
	Audit(context.Context, audit.Event) error
	NextID(context.Context, string) (int64, error)
	Receipt(context.Context, InquiryReceipt, time.Time) (InquiryReceipt, bool, error)
	InsertInquiry(context.Context, *Inquiry, *InquiryReceipt) error
	Inquiries(context.Context, int64, Query) ([]Inquiry, int64, error)
	Inquiry(context.Context, int64, int64, bool) (Inquiry, error)
	UpdateInquiry(context.Context, Inquiry, string, time.Time) error
	Visits(context.Context, int64, int64, Query) ([]Visit, int64, error)
}

// RecentAuthentication checks a current management session's strict five-minute window.
type RecentAuthentication func(context.Context, int64, int64, string) error

// InquiryService protects every human inquiry disclosure and behavior query.
type InquiryService struct {
	store       InquiryStore
	sealer      crypto.Sealer
	recent      RecentAuthentication
	now         func() time.Time
	idempotency idem.Store
}
