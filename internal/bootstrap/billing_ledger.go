package bootstrap

import (
	"context"
	"errors"
	"math"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
)

// Apply serializes only the operation identity; funds are always guarded by PG predicates.
func (s *billingStore) Apply(ctx context.Context, in billing.Change) (billing.Wallet, error) {
	if !db.InTransaction(ctx) || in.Currency != billing.Currency {
		return billing.Wallet{}, billing.Unavailable()
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if err := q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", in.IdempotencyKey).Error; err != nil {
		return billing.Wallet{}, billing.Unavailable()
	}
	var old billing.Txn
	err := q.Table("wallet_txn").Select(billingTxnColumns).Where("idempotency_key=?", in.IdempotencyKey).Take(&old).Error
	if err == nil {
		if old.TenantID != in.TenantID || old.WalletID != in.WalletID || old.RequestHash != in.RequestHash || old.Type != in.Type {
			return billing.Wallet{}, billing.Mismatch()
		}
		var w billing.Wallet
		if err := q.Table("wallet w").Select(billingWalletColumns).Where("w.tenant_id=? AND w.id=?", old.TenantID, old.WalletID).Take(&w).Error; err != nil {
			return w, billingDBError(err)
		}
		w.BalanceCents, w.FrozenCents, w.Version, w.UpdatedAt, w.Replayed = old.BalanceAfterCents, old.FrozenAfterCents, old.VersionAfter, old.OccurredAt, true
		return w, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return billing.Wallet{}, billing.Unavailable()
	}
	w, err := s.updateFunds(q, in)
	if err != nil {
		return w, err
	}
	in.BalanceAfterCents, in.FrozenAfterCents = w.BalanceCents, w.FrozenCents
	in.VersionAfter = w.Version
	if err := q.Table("wallet_txn").Create(&in.Txn).Error; err != nil {
		return billing.Wallet{}, billing.Unavailable()
	}
	return w, nil
}

func (s *billingStore) updateFunds(q *gorm.DB, in billing.Change) (billing.Wallet, error) {
	query := q.Table("wallet").Where("tenant_id=? AND id=? AND currency=?", in.TenantID, in.WalletID, in.Currency)
	if in.RequireActive {
		query = query.Where("status='active'")
	}
	if in.BalanceDeltaCents >= 0 {
		query = query.Where("balance_cents<=?", math.MaxInt64-in.BalanceDeltaCents)
	} else {
		query = query.Where("balance_cents>=?", -in.BalanceDeltaCents)
	}
	if in.FrozenDeltaCents >= 0 {
		query = query.Where("balance_cents-frozen_cents>=?", in.FrozenDeltaCents)
	} else {
		query = query.Where("frozen_cents>=?", -in.FrozenDeltaCents)
	}
	// Subtraction and addition in these predicates are bounded before UPDATE expressions run.
	query = query.Where("balance_cents::numeric + ? >= frozen_cents::numeric + ?", in.BalanceDeltaCents, in.FrozenDeltaCents)
	result := query.Updates(map[string]any{"balance_cents": gorm.Expr("balance_cents+?", in.BalanceDeltaCents), "frozen_cents": gorm.Expr("frozen_cents+?", in.FrozenDeltaCents), "version": gorm.Expr("version+1"), "updated_at": in.OccurredAt})
	if result.Error != nil {
		return billing.Wallet{}, billing.Unavailable()
	}
	var w billing.Wallet
	err := q.Table("wallet w").Select(billingWalletColumns).Where("w.tenant_id=? AND w.id=? AND w.currency=?", in.TenantID, in.WalletID, in.Currency).Take(&w).Error
	if err != nil {
		return w, billingDBError(err)
	}
	if result.RowsAffected == 1 {
		return w, nil
	}
	if in.RequireActive && w.Status != "active" {
		return w, billing.Inactive()
	}
	if in.FrozenDeltaCents < 0 {
		return w, billing.FrozenInsufficient()
	}
	if in.FrozenDeltaCents > 0 {
		return w, billing.Insufficient()
	}
	return w, billing.Invalid()
}
