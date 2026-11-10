package billing

import (
	"context"
	"time"
)

// Statement is a UTC calendar-month consumption bill, separate from funding.
type Statement struct {
	ID          int64      `json:"id"`
	TenantID    int64      `json:"-"`
	AppID       string     `json:"app_id"`
	Currency    string     `json:"currency"`
	PeriodStart time.Time  `json:"period_start"`
	PeriodEnd   time.Time  `json:"period_end"`
	Status      string     `json:"status"`
	TotalCents  int64      `json:"total_cents"`
	TotalUnits  int64      `json:"total_units"`
	CutoffAt    time.Time  `json:"cutoff_at"`
	SourceHash  string     `json:"source_hash"`
	Version     int        `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	IssuedAt    *time.Time `json:"issued_at,omitempty"`
	IssuedBy    *int64     `json:"issued_by,omitempty"`
}

// StatementLine preserves the minimum evidence after operational logs expire.
type StatementLine struct {
	ID                  int64     `json:"id"`
	TenantID            int64     `json:"-"`
	StatementID         int64     `json:"-"`
	SourceKey           string    `json:"source_key"`
	Kind                string    `json:"kind"`
	AmountCents         int64     `json:"amount_cents"`
	Units               int64     `json:"units"`
	PriceCents          int64     `json:"price_cents"`
	Quantity            int64     `json:"quantity"`
	RouteVersion        int       `json:"route_version"`
	EntitlementID       int64     `json:"entitlement_id,omitempty"`
	CallID              string    `json:"call_id,omitempty"`
	OriginalTxnID       int64     `json:"original_txn_id,omitempty"`
	OriginalStatementID int64     `json:"original_statement_id,omitempty"`
	ServiceDate         time.Time `json:"service_date"`
	OccurredAt          time.Time `json:"occurred_at"`
}

// StatementDetail pages immutable evidence without returning an unbounded body.
type StatementDetail struct {
	Statement
	Lines      []StatementLine `json:"lines"`
	NextCursor int64           `json:"next_cursor"`
}

// StatementFilter bounds owner-scoped list and detail reads.
type StatementFilter struct {
	AppID          string
	Page, PageSize int
	After          int64
}

// StatementPage is the management list envelope.
type StatementPage struct {
	Items    []Statement `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// StatementStore joins financial evidence and audit in the service transaction.
type StatementStore interface {
	Transaction(context.Context, func(context.Context) error) error
	BeginDraft(context.Context, Actor, string, time.Time, time.Time) (Statement, error)
	ClearLines(context.Context, Statement) error
	SourceLines(context.Context, Statement, string) ([]StatementLine, error)
	InsertLines(context.Context, Statement, []StatementLine) error
	SaveDraft(context.Context, *Statement) error
	OwnedStatement(context.Context, Actor, int64) (Statement, error)
	StatementLines(context.Context, Statement, int64, int) ([]StatementLine, error)
	Statements(context.Context, Actor, StatementFilter) (StatementPage, error)
	ReadyToIssue(context.Context, Statement) error
	MarkIssued(context.Context, *Statement, Actor, string, time.Time) error
	AuditStatement(context.Context, Actor, Statement, string) error
	Adjust(context.Context, Actor, AdjustmentInput, string, string, time.Time) (Adjustment, error)
	ApplyAdjustment(context.Context, Actor, int64, time.Time) (Adjustment, error)
	Adjustments(context.Context, Actor, StatementFilter) (AdjustmentPage, error)
}

// AdjustmentInput references the original settled cash operation and evidence.
type AdjustmentInput struct {
	OriginalTxnID int64  `json:"original_txn_id"`
	Kind          string `json:"kind"`
	AmountCents   int64  `json:"amount_cents"`
	DiffID        int64  `json:"diff_id"`
	Reason        string `json:"reason"`
	Evidence      string `json:"evidence"`
}

// Adjustment remains open until its ledger operation can durably commit.
type Adjustment struct {
	ID              int64  `json:"id"`
	TenantID        int64  `json:"-"`
	AppID           string `json:"app_id"`
	Currency        string `json:"currency"`
	AdjustmentInput `gorm:"embedded"`
	ActorID         int64      `json:"actor_id"`
	IdempotencyKey  string     `json:"-"`
	RequestHash     string     `json:"-"`
	Status          string     `json:"status"`
	PeriodStart     time.Time  `json:"period_start"`
	CreatedAt       time.Time  `json:"created_at"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
	LedgerTxnID     int64      `json:"ledger_txn_id,omitempty"`
}

// AdjustmentPage reports open and applied entries with the same owner scope.
type AdjustmentPage struct {
	Items    []Adjustment `json:"list"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}
