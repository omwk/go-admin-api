package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"gorm.io/gorm"
)

// Upserter has immutable dependencies and must run inside the caller's transaction.
type Upserter struct {
	sealer crypto.Sealer
	policy MoneyPolicy
}

// NewUpserter requires explicit tolerances; it does not install any HTTP or sync handlers.
func NewUpserter(sealer crypto.Sealer, policy MoneyPolicy) (*Upserter, error) {
	if sealer == nil || policy.FormulaToleranceCents < 0 || policy.PaymentToleranceCents < 0 {
		return nil, errors.New("order sealer and nonnegative tolerances are required")
	}
	return &Upserter{sealer: sealer, policy: policy}, nil
}

// Apply atomically updates all order facts in tx. Callers must propagate every error
// and commit the checkpoint in that same transaction after its durable disposition.
func (u *Upserter) Apply(ctx context.Context, tx *gorm.DB, s Snapshot) (Result, error) {
	if err := requireTransaction(ctx, tx, u); err != nil {
		return Result{}, err
	}
	if err := validateIdentity(s); err != nil {
		return Result{}, err
	}
	if s.Mode == "" {
		s.Mode = ChangeSnapshot
	}
	tx = tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx)
	if err := verifyRelationships(tx, s, u.sealer); err != nil {
		return Result{}, err
	}
	old, err := loadOrder(tx, s)
	if err != nil {
		return Result{}, err
	}
	hash, err := snapshotHash(s)
	if err != nil {
		return Result{}, err
	}
	if indexer, ok := u.sealer.(crypto.BlindIndexer); ok {
		hash, err = indexer.BlindIndex("order/content/v1", []byte(hash))
		if err != nil {
			return Result{}, err
		}
	}
	source := sourceDecision(old, s, hash)
	if source == "skipped_same" {
		return Result{OrderID: old.ID, Version: old.Version, Outcome: source}, nil
	}
	if old.Version != s.ExpectedVersion {
		return Result{}, versionConflict()
	}
	if old.DeletedAt != nil {
		return Result{}, errs.New(50302, 409, "订单已关闭不可操作")
	}
	if source == "skipped_stale" {
		return Result{OrderID: old.ID, Version: old.Version, Outcome: source}, nil
	}
	if source != "" {
		return recordConflict(tx, old, s, StateDecision{Reason: source})
	}
	rows, deltas, reason := u.prepare(old, s)
	if reason != "" {
		return quarantine(tx, old, s, deltas, reason)
	}
	decision, err := decideAggregate(old, s)
	if err != nil {
		return Result{}, err
	}
	if decision.Reason != "" {
		return recordConflict(tx, old, s, decision)
	}
	if reason, err := validateExistingItems(tx, old, s, correctionProved(old, s)); err != nil {
		return Result{}, err
	} else if reason != "" {
		return recordConflict(tx, old, s, StateDecision{Reason: reason})
	}
	return u.persist(tx, old, s, hash, deltas, rows, decision)
}

func requireTransaction(ctx context.Context, tx *gorm.DB, u *Upserter) error {
	if u == nil || tx == nil || tx.Statement == nil {
		return errors.New("order transaction is required")
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return errors.New("order transaction is required")
	}
	return ctx.Err()
}

func versionConflict() error { return errs.New(errs.ErrVersionConflict, 409, "订单版本冲突") }
func identityKey(tenant, shop int64, external string) string {
	return fmt.Sprintf("%d:%d:%s", tenant, shop, external)
}

func (u *Upserter) persist(tx *gorm.DB, old Order, s Snapshot, hash string, deltas Deltas, items []Item, decision StateDecision) (Result, error) {
	now := time.Now().UTC()
	row := orderRow(old, s, hash, deltas, now)
	if err := saveOrder(tx, &row, old); err != nil {
		return Result{}, err
	}
	if s.Items != nil {
		if err := replaceItems(tx, row, items, now); err != nil {
			return Result{}, err
		}
	}
	if s.Address != nil {
		if err := u.writeAddress(tx, row, *s.Address); err != nil {
			return Result{}, err
		}
	}
	reason := "observed"
	if old.ID != 0 && old.RawID == s.Source.RawID && old.MappingChecksum != s.Source.MappingChecksum {
		decision.Changes = append(decision.Changes, StateChange{"aggregate", "interpreted", "reinterpreted"})
		reason = "mapping_reinterpretation"
	}
	if s.Mode == ChangeCorrection && len(decision.Changes) == 0 {
		decision.Changes = []StateChange{{"aggregate", "retained", "corrected"}}
	}
	if err := appendHistory(tx, row.ID, s, decision.Changes, string(s.Mode), reason, now); err != nil {
		return Result{}, err
	}
	return Result{OrderID: row.ID, Version: row.Version, Outcome: "applied", Deltas: deltas}, nil
}
