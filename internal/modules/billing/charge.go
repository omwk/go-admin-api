package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// Service owns transaction boundaries for all cash operations.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService requires primary persistence and an explicit clock.
func NewService(store Store, now func() time.Time) (*Service, error) {
	if store == nil || now == nil {
		return nil, errors.New("billing persistence and clock required")
	}
	return &Service{store: store, now: now}, nil
}

// Prepare creates the per-call reservation before technical quota locks are taken.
// The caller must include Prepare and Freeze in one outer admission transaction.
func (s *Service) Prepare(ctx context.Context, c Charge) error {
	amount, err := ChargeAmount(c.PriceCents, c.Units)
	if err != nil || c.Currency != Currency || c.CallID == "" || c.AppID == "" || c.TenantID <= 0 || !c.DeadlineAt.After(c.CreatedAt) {
		return Invalid()
	}
	c.PreCents, c.State = amount, "reserved"
	return s.store.Prepare(ctx, &c)
}

// Freeze atomically records an exact reservation and its frozen-funds ledger entry.
func (s *Service) Freeze(ctx context.Context, c Charge) error {
	return s.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Prepare(tx, c); err != nil {
			return err
		}
		current, err := s.store.Charge(tx, c.TenantID, c.CallID)
		if err != nil {
			return err
		}
		// Replaying freeze after completion must never freeze the wallet again.
		if current.State != "reserved" {
			return nil
		}
		_, err = s.store.Apply(tx, Change{Txn: Txn{TenantID: c.TenantID, WalletID: current.WalletID, Type: "freeze", AmountCents: current.PreCents,
			FrozenDeltaCents: current.PreCents, CallID: c.CallID, IdempotencyKey: chargeKey(c, "freeze"), RequestHash: fingerprint(current.CallID, current.PreCents), OccurredAt: s.now().UTC()}, Currency: c.Currency, RequireActive: true})
		return err
	})
}

// Settle removes this call's full reservation and debits only the actual amount.
func (s *Service) Settle(ctx context.Context, tenant int64, call string, actual int64) error {
	return s.complete(ctx, tenant, call, "settled", actual)
}

// Release frees a failed or expired reservation without reducing book balance.
func (s *Service) Release(ctx context.Context, tenant int64, call string, expired bool) error {
	state := "released"
	if expired {
		state = "expired"
	}
	return s.complete(ctx, tenant, call, state, 0)
}

func (s *Service) complete(ctx context.Context, tenant int64, id, state string, actual int64) error {
	if actual < 0 {
		return Invalid()
	}
	return s.store.Transaction(ctx, func(tx context.Context) error {
		c, err := s.store.Charge(tx, tenant, id)
		if err != nil {
			return err
		}
		if c.State != "reserved" {
			if c.State == state && c.ActualCents == actual {
				return nil
			}
			return TerminalConflict()
		}
		// M9a never adds funds after admission; variable pricing is outside this contract.
		if actual > c.PreCents {
			return Invalid()
		}
		kind := "release"
		amount := c.PreCents
		if state == "settled" {
			kind = "settle"
			amount = actual
		}
		at := s.now().UTC()
		_, err = s.store.Apply(tx, Change{Txn: Txn{TenantID: tenant, WalletID: c.WalletID, Type: kind, AmountCents: amount,
			BalanceDeltaCents: -actual, FrozenDeltaCents: -c.PreCents, CallID: id, IdempotencyKey: chargeKey(c, kind), RequestHash: fingerprint(id, state, actual, c.PreCents), OccurredAt: at}, Currency: c.Currency})
		if err != nil {
			return err
		}
		c.State, c.ActualCents, c.FinishedAt = state, actual, &at
		return s.store.EndCharge(tx, c)
	})
}

// Confirm checks the durable financial receipt when a commit acknowledgement was lost.
func (s *Service) Confirm(ctx context.Context, expected Charge, state string) error {
	c, err := s.store.Charge(ctx, expected.TenantID, expected.CallID)
	if err != nil {
		return err
	}
	if !SameCharge(c, expected) || c.State != state || state == "settled" && c.ActualCents != c.PreCents {
		return Unavailable()
	}
	return nil
}

// SameCharge compares immutable attribution and the admission price snapshot.
func SameCharge(a, b Charge) bool {
	return a.CallID == b.CallID && a.TenantID == b.TenantID && a.AppID == b.AppID && a.Currency == b.Currency && a.PriceCents == b.PriceCents && a.Units == b.Units && a.RouteID == b.RouteID && a.RouteVersion == b.RouteVersion
}

func chargeKey(c Charge, action string) string {
	return "billing:call:" + strconv.FormatInt(c.TenantID, 10) + ":" + c.CallID + ":" + action
}

func fingerprint(values ...any) string {
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
