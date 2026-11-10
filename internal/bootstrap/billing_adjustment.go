package bootstrap

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *billingStatementStore) Adjust(ctx context.Context, a billing.Actor, in billing.AdjustmentInput, key, hash string, at time.Time) (billing.Adjustment, error) {
	original, w, err := s.adjustmentOriginal(ctx, a, in.OriginalTxnID)
	if err != nil {
		return billing.Adjustment{}, err
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Adjustment
	err = q.Table("billing_adjustment").Select(adjustmentColumns).Where("tenant_id=? AND idempotency_key=?", a.TenantID, key).Take(&out).Error
	if err == nil {
		if out.RequestHash != hash {
			return out, billing.Mismatch()
		}
		return out, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, billing.Unavailable()
	}
	if in.Kind == "refund" {
		var refunded int64
		if err := q.Table("billing_adjustment").Select("coalesce(sum(amount_cents),0)").Where("tenant_id=? AND original_txn_id=? AND kind='refund'", a.TenantID, original.ID).Scan(&refunded).Error; err != nil {
			return out, billing.Unavailable()
		}
		if in.AmountCents > original.AmountCents-refunded {
			return out, billing.Invalid()
		}
	}
	if err := s.checkAdjustmentDifference(ctx, a, w.AppID, original.CallID, in.DiffID); err != nil {
		return out, err
	}
	period, err := s.adjustmentPeriod(ctx, a, w.AppID, at)
	if err != nil {
		return out, err
	}
	out = billing.Adjustment{TenantID: a.TenantID, AppID: w.AppID, Currency: w.Currency, AdjustmentInput: in, ActorID: a.UserID, IdempotencyKey: key, RequestHash: hash, Status: "open", PeriodStart: period, CreatedAt: at}
	if err := q.Table("billing_adjustment").Create(&out).Error; err != nil {
		return out, billing.Unavailable()
	}
	if err := s.auditAdjustment(ctx, a, out, "billing.adjustment.open"); err != nil {
		return out, err
	}
	return s.applyAdjustment(ctx, a, out, original, w, at)
}

func (s *billingStatementStore) adjustmentOriginal(ctx context.Context, a billing.Actor, id int64) (billing.Txn, billing.Wallet, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var original billing.Txn
	err := q.Table("wallet_txn").Select(billingTxnColumns).Where("tenant_id=? AND id=? AND type='settle' AND biz_type='api_call'", a.TenantID, id).Take(&original).Error
	if err != nil {
		return original, billing.Wallet{}, billingDBError(err)
	}
	w, err := (&billingStore{database: s.database}).OwnedWallet(ctx, a, original.WalletID)
	if err != nil {
		return original, w, err
	}
	if err := s.lockApp(ctx, a, w.AppID); err != nil {
		return original, w, err
	}
	err = q.Table("wallet_txn").Select(billingTxnColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", a.TenantID, id).Take(&original).Error
	return original, w, billingDBError(err)
}

func (s *billingStatementStore) ApplyAdjustment(ctx context.Context, a billing.Actor, id int64, at time.Time) (billing.Adjustment, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var out billing.Adjustment
	if err := ownedStatementQuery(q, a, "billing_adjustment").Select(adjustmentColumns).Where("id=?", id).Take(&out).Error; err != nil {
		return out, billingDBError(err)
	}
	original, w, err := s.adjustmentOriginal(ctx, a, out.OriginalTxnID)
	if err != nil {
		return out, err
	}
	if err := q.Table("billing_adjustment").Select(adjustmentColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", a.TenantID, id).Take(&out).Error; err != nil {
		return out, billingDBError(err)
	}
	if out.Status == "applied" {
		return out, nil
	}
	if err := s.checkAdjustmentDifference(ctx, a, out.AppID, original.CallID, out.DiffID); err != nil {
		return out, err
	}
	return s.applyAdjustment(ctx, a, out, original, w, at)
}

func (s *billingStatementStore) applyAdjustment(ctx context.Context, a billing.Actor, out billing.Adjustment, original billing.Txn, w billing.Wallet, at time.Time) (billing.Adjustment, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	delta, kind := out.AmountCents, "refund"
	if out.Kind == "supplement" {
		delta, kind = -out.AmountCents, "adjust"
	}
	ledgerKey := "billing:adjustment:" + strconv.FormatInt(a.TenantID, 10) + ":" + strconv.FormatInt(out.ID, 10)
	_, err := (&billingStore{database: s.database}).Apply(ctx, billing.Change{Txn: billing.Txn{TenantID: a.TenantID, WalletID: w.ID, Type: kind, AmountCents: delta, BalanceDeltaCents: delta, IdempotencyKey: ledgerKey, RequestHash: out.RequestHash, ActorID: a.UserID, Note: out.Reason, OccurredAt: at, BizType: "compensation", BizID: strconv.FormatInt(out.ID, 10), ServiceDate: original.ServiceDate}, Currency: w.Currency, RequireActive: true})
	if err != nil {
		if out.Kind == "supplement" && errs.From(err).Code == 90102 {
			return out, nil
		}
		return out, err
	}
	var ledger billing.Txn
	if err := q.Table("wallet_txn").Select(billingTxnColumns).Where("idempotency_key=?", ledgerKey).Take(&ledger).Error; err != nil {
		return out, billingDBError(err)
	}
	period, err := s.adjustmentPeriod(ctx, a, w.AppID, at)
	if err != nil {
		return out, err
	}
	result := q.Table("billing_adjustment").Where("tenant_id=? AND id=? AND status='open'", a.TenantID, out.ID).Updates(map[string]any{"status": "applied", "period_start": period, "applied_at": at, "ledger_txn_id": ledger.ID})
	if result.Error != nil || result.RowsAffected != 1 {
		return out, billing.Unavailable()
	}
	out.Status, out.PeriodStart, out.AppliedAt, out.LedgerTxnID = "applied", period, &at, ledger.ID
	if out.DiffID > 0 {
		if err := s.resolveAdjustmentDifference(ctx, a, out, original, at); err != nil {
			return out, err
		}
	}
	return out, s.auditAdjustment(ctx, a, out, "billing.adjustment.apply")
}

func (s *billingStatementStore) adjustmentPeriod(ctx context.Context, a billing.Actor, app string, at time.Time) (time.Time, error) {
	period := time.Date(at.UTC().Year(), at.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var last struct{ PeriodEnd *time.Time }
	if err := q.Table("billing_statement").Select("max(period_end) period_end").Where("tenant_id=? AND app_id=? AND status='issued'", a.TenantID, app).Scan(&last).Error; err != nil {
		return period, billing.Unavailable()
	}
	if last.PeriodEnd != nil && last.PeriodEnd.After(period) {
		period = *last.PeriodEnd
	}
	return period, nil
}

func (s *billingStatementStore) auditAdjustment(ctx context.Context, a billing.Actor, out billing.Adjustment, action string) error {
	return audit.Record(ctx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: action, ResourceType: "billing_adjustment", ResourceID: strconv.FormatInt(out.ID, 10), After: map[string]any{"original_txn_id": out.OriginalTxnID, "kind": out.Kind, "amount_cents": out.AmountCents, "status": out.Status, "diff_id": out.DiffID, "evidence": out.Evidence}, Result: "success"})
}
