package billing

import (
	"context"
	"time"
)

// Currency is the only settlement currency supported by M9a.
const Currency = "CNY"

// Actor identifies the human responsible for an owned application's funding.
type Actor struct{ TenantID, UserID int64 }

// Wallet reports book balance and its reserved portion in integer cents.
type Wallet struct {
	Replayed     bool      `json:"-" gorm:"-"`
	ID           int64     `json:"id"`
	TenantID     int64     `json:"-"`
	AppID        string    `json:"app_id"`
	Currency     string    `json:"currency"`
	BalanceCents int64     `json:"balance_cents"`
	FrozenCents  int64     `json:"frozen_cents"`
	Version      int       `json:"version"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Balance is the explicit public wallet projection.
type Balance struct {
	Currency       string `json:"currency"`
	BalanceCents   int64  `json:"balance_cents"`
	AvailableCents int64  `json:"available_cents"`
	FrozenCents    int64  `json:"frozen_cents"`
}

// View computes available funds without changing the book balance.
func (w Wallet) View() Balance {
	return Balance{Currency: w.Currency, BalanceCents: w.BalanceCents, AvailableCents: w.BalanceCents - w.FrozenCents, FrozenCents: w.FrozenCents}
}

// Txn is an immutable operation and its resulting balance snapshot.
type Txn struct {
	ID, TenantID, WalletID                           int64
	Type                                             string
	AmountCents, BalanceDeltaCents, FrozenDeltaCents int64
	BalanceAfterCents, FrozenAfterCents              int64
	VersionAfter                                     int
	CallID, IdempotencyKey, RequestHash              string
	ActorID                                          int64
	Note                                             string
	OccurredAt                                       time.Time
	BizType, BizID                                   string
	ServiceDate                                      *time.Time
}

// Change is a conditional ledger command, never a read-modify-write balance.
type Change struct {
	Txn
	Currency      string
	RequireActive bool
}

// Charge freezes the exact admission price until a mutually exclusive terminal state.
type Charge struct {
	CallID                                   string `gorm:"primaryKey"`
	TenantID, WalletID                       int64
	AppID, Currency                          string
	RouteID                                  int64
	RouteVersion                             int
	PriceCents, Units, PreCents, ActualCents int64
	State                                    string
	Source                                   string
	EntitlementID, ReservedUnits             int64
	CreatedAt, DeadlineAt                    time.Time
	FinishedAt                               *time.Time
}

// Code stores only a one-way digest of a random recharge credential.
type Code struct {
	ID, TenantID      int64
	CodeHash          string
	AmountCents       int64
	Currency, Status  string
	UsedByAppID       string
	UsedAt, ExpiresAt *time.Time
	CreatedBy         int64
	CreatedAt         time.Time
}

// CodeDelivery is delivered once; plaintext never enters audit or replay storage.
type CodeDelivery struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
}

// Store participates in the caller's primary-database transaction context.
type Store interface {
	EntitlementStore
	Transaction(context.Context, func(context.Context) error) error
	OpenWallet(context.Context, Actor, string, time.Time) (Wallet, error)
	OwnedWallet(context.Context, Actor, int64) (Wallet, error)
	Wallet(context.Context, int64, string) (Wallet, error)
	Wallets(context.Context, Actor) ([]Wallet, error)
	Txns(context.Context, Actor, int64) ([]Txn, error)
	Apply(context.Context, Change) (Wallet, error)
	Prepare(context.Context, *Charge) error
	Charge(context.Context, int64, string) (Charge, error)
	EndCharge(context.Context, Charge) error
	InsertCode(context.Context, *Code) error
	UseCode(context.Context, int64, string, string, time.Time) (Code, error)
	AuditCredit(context.Context, Actor, Wallet, Wallet, string) error
}
