package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/billing"
)

func (s *billingStatementStore) ReadyToIssue(ctx context.Context, st billing.Statement) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var ready bool
	err := q.Raw(`SELECT
 EXISTS(SELECT 1 FROM billing_reconcile_month WHERE tenant_id=? AND period_start=? AND covered_through>=?)
 AND NOT EXISTS(SELECT 1 FROM billing_reconcile_diff WHERE tenant_id=? AND app_id=? AND status='open' AND service_date<?)
 AND NOT EXISTS(SELECT 1 FROM call_charge WHERE tenant_id=? AND app_id=? AND state='reserved' AND service_date<?)
 AND NOT EXISTS(SELECT 1 FROM billing_adjustment WHERE tenant_id=? AND app_id=? AND status='open' AND period_start<?)
 AND NOT EXISTS(SELECT 1 FROM billing_statement_line l WHERE l.tenant_id=? AND l.statement_id=? AND NOT EXISTS(
 SELECT 1 FROM billing_reconcile_run r WHERE r.tenant_id=l.tenant_id AND r.service_date=l.service_date AND r.status='completed' AND r.trigger<>'compensation' AND r.cutoff_at>=l.occurred_at))`,
		st.TenantID, st.PeriodStart, st.PeriodEnd, st.TenantID, st.AppID, st.PeriodEnd, st.TenantID, st.AppID, st.PeriodEnd, st.TenantID, st.AppID, st.PeriodEnd, st.TenantID, st.ID).Row().Scan(&ready)
	if err != nil {
		return billing.Unavailable()
	}
	if !ready {
		return billing.TerminalConflict()
	}
	return nil
}

func (s *billingStatementStore) MarkIssued(ctx context.Context, st *billing.Statement, a billing.Actor, reason string, at time.Time) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	result := q.Table("billing_statement").Where("tenant_id=? AND id=? AND status='draft' AND version=?", st.TenantID, st.ID, st.Version).Updates(map[string]any{"status": "issued", "issued_at": at, "issued_by": a.UserID, "updated_at": at, "version": st.Version + 1})
	if result.Error != nil {
		return billing.Unavailable()
	}
	if result.RowsAffected != 1 {
		return billing.TerminalConflict()
	}
	st.Status, st.IssuedAt, st.IssuedBy, st.UpdatedAt = "issued", &at, &a.UserID, at
	st.Version++
	if err := s.AuditStatement(ctx, a, *st, "billing.statement.issue"); err != nil {
		return err
	}
	return audit.Record(ctx, s.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: "billing.statement.issue.reason", ResourceType: "billing_statement", ResourceID: st.SourceHash, After: map[string]any{"statement_id": st.ID, "reason": reason}, Result: "success"})
}
