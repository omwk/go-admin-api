package billing

import (
	"context"
	"time"
)

// Plan is a versioned offer; a purchase copies its terms into an entitlement.
type Plan struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"-"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	PriceCents int64  `json:"price_cents"`
	QuotaType  string `json:"quota_type"`
	UnitCount  int64  `json:"unit_count"`
	ValidDays  int    `json:"valid_days"`
	RouteID    int64  `json:"route_id"`
	Status     string `json:"status"`
	Version    int    `json:"version"`
}

// Entitlement keeps unconsumed units separate from its in-flight reservations.
type Entitlement struct {
	ID             int64      `json:"id"`
	TenantID       int64      `json:"-"`
	AppID          string     `json:"app_id"`
	Currency       string     `json:"currency"`
	Kind           string     `json:"kind"`
	QuotaType      string     `json:"quota_type"`
	TotalUnits     int64      `json:"total_units"`
	RemainingUnits int64      `json:"remaining_units"`
	ReservedUnits  int64      `json:"reserved_units"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
	RouteID        int64      `json:"route_id"`
	Source         string     `json:"source"`
	SourceRef      string     `json:"source_ref"`
	PlanID         int64      `json:"plan_id"`
	PlanVersion    int        `json:"plan_version"`
	PurchaseCents  int64      `json:"purchase_cents"`
	Status         string     `json:"status"`
	Version        int        `json:"version"`
	CreatedBy      int64      `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
	GrantKey       string     `json:"-"`
	RequestHash    string     `json:"-"`
}

// GrantInput either purchases a plan or grants a separately attributed gift.
type GrantInput struct {
	AppID     string     `json:"app_id"`
	PlanID    int64      `json:"plan_id"`
	Kind      string     `json:"kind"`
	QuotaType string     `json:"quota_type"`
	Units     int64      `json:"units"`
	RouteID   int64      `json:"route_id"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	SourceRef string     `json:"source_ref"`
	AutoRenew bool       `json:"auto_renew"`
}

// UnitTxn is immutable unit movement with both UTC business and posting days.
type UnitTxn struct {
	ID, TenantID, EntitlementID           int64
	AppID, Currency, CallID, Type         string
	Amount, RemainingDelta, ReservedDelta int64
	RemainingAfter, ReservedAfter         int64
	VersionAfter                          int
	IdempotencyKey                        string
	OccurredAt, ServiceDate               time.Time
}

// EntitlementStore joins the same transaction as the cash and call ledgers.
type EntitlementStore interface {
	ReserveSource(context.Context, *Charge, time.Time) error
	FinishUnits(context.Context, Charge, string, time.Time) error
	ExpireUnits(context.Context, time.Time) (int, error)
	SavePlan(context.Context, Actor, Plan) (Plan, error)
	Plans(context.Context, Actor) ([]Plan, error)
	Grant(context.Context, Actor, GrantInput, string, string, time.Time) (Entitlement, error)
	Entitlements(context.Context, Actor, string) ([]Entitlement, error)
	UnitTxns(context.Context, Actor, int64) ([]UnitTxn, error)
}
