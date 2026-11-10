package bootstrap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"gorm.io/gorm/clause"
)

func (s *billingStatementStore) checkAdjustmentDifference(ctx context.Context, a billing.Actor, app, call string, id int64) error {
	if id == 0 {
		return nil
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var d billing.Difference
	err := ownedStatementQuery(q, a, "billing_reconcile_diff").Select(reconcileDiffColumns).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&d).Error
	if err != nil {
		return billingDBError(err)
	}
	if d.Status != "open" || d.AppID != app || d.CallID != "" && d.CallID != call {
		return billing.TerminalConflict()
	}
	return nil
}

func (s *billingStatementStore) resolveAdjustmentDifference(ctx context.Context, a billing.Actor, out billing.Adjustment, original billing.Txn, at time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var d billing.Difference
	if err := q.Table("billing_reconcile_diff").Select(reconcileDiffColumns).Where("tenant_id=? AND id=? AND status='open'", a.TenantID, out.DiffID).Take(&d).Error; err != nil {
		return billingDBError(err)
	}
	before, err := json.Marshal(map[string]any{"expected": d.Expected, "actual": d.Actual, "original_txn_id": original.ID})
	if err != nil {
		return billing.Unavailable()
	}
	after, err := json.Marshal(map[string]any{"adjustment_id": out.ID, "ledger_txn_id": out.LedgerTxnID, "evidence": out.Evidence, "kind": out.Kind, "amount_cents": out.AmountCents})
	if err != nil {
		return billing.Unavailable()
	}
	// This is an explicit human disposition backed by a committed adjustment,
	// not a rewrite or a claim that the original observation has disappeared.
	credential := billing.Compensation{TenantID: a.TenantID, DiffID: d.ID, ActorID: a.UserID, OccurredAt: at, Action: "financial_adjustment", Reason: out.Reason, BeforeValue: before, AfterValue: after, IdempotencyKey: "adjustment:" + out.IdempotencyKey, VerificationRunID: d.RunID}
	if err := q.Table("billing_compensation").Create(&credential).Error; err != nil {
		return billing.Unavailable()
	}
	result := q.Table("billing_reconcile_diff").Where("tenant_id=? AND id=? AND status='open'", a.TenantID, d.ID).Updates(map[string]any{"status": "resolved", "resolved_by": a.UserID, "resolved_at": at})
	if result.Error != nil || result.RowsAffected != 1 {
		return billing.Unavailable()
	}
	return nil
}

func (s *billingStatementStore) Adjustments(ctx context.Context, a billing.Actor, f billing.StatementFilter) (billing.AdjustmentPage, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	out := billing.AdjustmentPage{Items: []billing.Adjustment{}, Page: f.Page, PageSize: f.PageSize}
	query := ownedStatementQuery(q, a, "billing_adjustment")
	if f.AppID != "" {
		query = query.Where("app_id=?", f.AppID)
	}
	if err := query.Count(&out.Total).Error; err != nil {
		return out, billing.Unavailable()
	}
	err := query.Select(adjustmentColumns).Order("created_at DESC,id DESC").Offset((f.Page - 1) * f.PageSize).Limit(f.PageSize).Find(&out.Items).Error
	return out, billingDBError(err)
}
