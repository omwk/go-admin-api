package billing

import (
	"context"
	"encoding/json"
	"time"
)

// ReconcileRun is the cutoff and progress watermark for a dated observation.
type ReconcileRun struct {
	CommandKey  string     `json:"-"`
	ID          int64      `json:"id"`
	TenantID    int64      `json:"-"`
	ServiceDate time.Time  `json:"service_date"`
	PostingDate time.Time  `json:"posting_date"`
	CutoffAt    time.Time  `json:"cutoff_at"`
	ActorID     int64      `json:"actor_id"`
	Trigger     string     `json:"trigger"`
	Status      string     `json:"status"`
	LastCallID  string     `json:"last_call_id"`
	Calls       int64      `json:"calls"`
	Differences int64      `json:"differences"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// Difference preserves exact integer observations; resolution never rewrites them.
type Difference struct {
	ID          int64      `json:"id"`
	RunID       int64      `json:"run_id"`
	TenantID    int64      `json:"-"`
	AppID       string     `json:"app_id"`
	Currency    string     `json:"currency"`
	ServiceDate time.Time  `json:"service_date"`
	PostingDate time.Time  `json:"posting_date"`
	CallID      string     `json:"call_id"`
	Kind        string     `json:"kind"`
	Expected    int64      `json:"expected"`
	Actual      int64      `json:"actual"`
	ReferenceID int64      `json:"reference_id"`
	Fingerprint string     `json:"-"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy  *int64     `json:"resolved_by,omitempty"`
}

// CallFact aggregates each independent side without multiplying joined rows.
type CallFact struct {
	CallID, AppID, Currency, State, BillingMode, Source, ChargeState                            string
	Admitted                                                                                    bool
	CallCount, LogCount, MeterCount, MeterUnits, ChargeCount                                    int64
	PriceCents, Units, PreCents, ActualCents, ReservedUnits                                     int64
	CashCount, CashCents, FreezeCount, FreezeCents, ReleaseCount, UnfrozenCents                 int64
	ConsumeCount, ConsumedUnits, UnitReserveCount, UnitReserved, UnitReleaseCount, UnitReleased int64
	LogCents, LogUnits, InvalidRefs                                                             int64
	DeadlineAt                                                                                  time.Time
}

// ControlFact compares a ledger against its balance, snapshots or purchase grant.
type ControlFact struct {
	AppID, Currency, Kind         string
	ReferenceID, Expected, Actual int64
}

// Compensation is the immutable who/when/why/before/after repair credential.
type Compensation struct {
	ID                int64           `json:"id"`
	TenantID          int64           `json:"-"`
	DiffID            int64           `json:"diff_id"`
	ActorID           int64           `json:"actor_id"`
	OccurredAt        time.Time       `json:"occurred_at"`
	Action            string          `json:"action"`
	Reason            string          `json:"reason"`
	BeforeValue       json.RawMessage `json:"before_value" gorm:"type:jsonb"`
	AfterValue        json.RawMessage `json:"after_value" gorm:"type:jsonb"`
	IdempotencyKey    string          `json:"-"`
	VerificationRunID int64           `json:"verification_run_id"`
}

// ReconcileStore persists bounded batches and only repairs with durable evidence.
type ReconcileStore interface {
	DailyUsage(context.Context, Actor, string) ([]DailyUsage, error)
	BeginRun(context.Context, ReconcileRun) (ReconcileRun, error)
	Facts(context.Context, ReconcileRun, string) ([]CallFact, error)
	Controls(context.Context, ReconcileRun) ([]ControlFact, error)
	Observe(context.Context, ReconcileRun, []Difference, string, int64) error
	CompleteRun(context.Context, ReconcileRun, time.Time) (ReconcileRun, error)
	PendingAlerts(context.Context, int64, int64) ([]Difference, error)
	AcknowledgeAlert(context.Context, int64) error
	Differences(context.Context, Actor, string) ([]Difference, error)
	Compensate(context.Context, Actor, int64, string, string, time.Time) (Compensation, error)
	Compensations(context.Context, Actor, int64) ([]Compensation, error)
}

// DailyUsage is a recomputed service-day summary, never a cash purchase allocation.
type DailyUsage struct {
	TenantID         int64     `json:"-"`
	AppID            string    `json:"app_id"`
	Currency         string    `json:"currency"`
	ServiceDate      time.Time `json:"service_date"`
	PostingDate      time.Time `json:"posting_date"`
	Calls            int64     `json:"calls"`
	MeteredCalls     int64     `json:"metered_calls"`
	MeteredCents     int64     `json:"metered_cents"`
	ChargedCents     int64     `json:"charged_cents"`
	EntitlementUnits int64     `json:"entitlement_units"`
	RunID            int64     `json:"run_id"`
	CutoffAt         time.Time `json:"cutoff_at"`
}
