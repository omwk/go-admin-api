package bootstrap

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestIdentityAuditSemanticsIntegrationSearchSnapshots(t *testing.T) {
	// BE-IDN-009 §4.12–16: HTTP queries search only tenant-bound persisted names.
	f := newSystemFixture(t, nil)
	baselineAdmin := s4AuditPage(t, f, "keyword=admin").Total
	actor, tenant, label := "audit-admin", "default", "old-department"
	row := insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "user", ActorID: &f.user.ID,
		ActorName: &actor, ActorTenantName: &tenant, ResourceLabel: &label, Action: "system.tenant.rename",
		ResourceType: "tenant", ResourceID: "1", Result: "success", CreatedAt: f.now})
	insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "user", ActorName: &actor, Action: "system.user.update", ResourceType: "user", ResourceID: "1", Result: "success", CreatedAt: f.now})
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(code,name) VALUES('audit-foreign','Foreign')"))
	insertS4Audit(t, f, 2, system.AuditRecord{ActorType: "user", ActorName: &actor, Action: row.Action, ResourceType: "tenant", ResourceID: "1", Result: "success", CreatedAt: f.now})
	for query, expected := range map[string]int64{
		"keyword=ADMIN": baselineAdmin + 2, "keyword=old-department": 1, "actor_name=audit-admin": 2,
		"actor_name=audit": 0, "action_prefix=system.tenant.": 1,
		"keyword=admin&action_prefix=system.tenant.": 1, "keyword=%25": 0, "action_prefix=system.%25": 0,
	} {
		page := s4AuditPage(t, f, query)
		require.Equal(t, expected, page.Total, query)
	}
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET username='new-audit-actor' WHERE id=?", f.user.ID))
	page := s4AuditPage(t, f, "keyword="+url.QueryEscape(label))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, &actor, page.List[0].ActorName)
	require.Equal(t, &tenant, page.List[0].ActorTenantName)
	require.Equal(t, &label, page.List[0].ResourceLabel)
	t.Log("HTTP keyword=ADMIN hits actor snapshots; action_prefix=system.tenant. returns only tenant actions; literal wildcard, exact actor and tenant isolation verified")
}

func TestIdentityAuditSemanticsIntegrationPruneBoundaryAndRollback(t *testing.T) {
	// BE-IDN-009 §4.11: strict cutoff; failed outer transaction cannot prune evidence.
	f := newSystemFixture(t, nil)
	cutoff := f.now.Add(time.Hour)
	for _, at := range []time.Time{cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond)} {
		insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "system", Action: "identity.reauth", ResourceType: "user", ResourceID: "1", Result: "success", CreatedAt: at})
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var before int64
	require.NoError(t, q.Table("audit_log").Count(&before).Error)
	rollback := errors.New("rollback prune")
	require.ErrorIs(t, f.database.Transaction(f.ctx, func(tx context.Context) error {
		count, err := PruneAudit(tx, f.database, cutoff)
		require.NoError(t, err)
		require.Equal(t, before-2, count)
		return rollback
	}), rollback)
	var total int64
	require.NoError(t, q.Table("audit_log").Count(&total).Error)
	require.Equal(t, before, total)
	count, err := PruneAudit(f.ctx, f.database, cutoff)
	require.NoError(t, err)
	require.Equal(t, before-2, count)
	require.NoError(t, q.Table("audit_log").Count(&total).Error)
	require.EqualValues(t, 2, total)
	require.Error(t, f.database.Exec(f.ctx, "DELETE FROM audit_log"))
	require.Error(t, f.database.Exec(f.ctx, "UPDATE audit_log SET action='identity.login'"))
	require.Error(t, f.database.Exec(f.ctx, "TRUNCATE audit_log"))
	t.Logf("prune deleted=%d remaining=%d; exactly-at-cutoff retained; rollback restored all rows; ordinary DELETE/UPDATE/TRUNCATE rejected", count, total)
}

func TestIdentityAuditSemanticsIntegrationFailureSanitizedAtSink(t *testing.T) {
	f := newSystemFixture(t, nil)
	event := audit.Event{TenantID: 1, ActorType: "user", ActorID: &f.user.ID, Action: "identity.reauth", ResourceType: "user", ResourceID: "1"}
	audit.Failure(&event, errors.New("untrusted diagnostic"))
	require.NoError(t, RecordAudit(f.ctx, f.database, event))
	page := s4AuditPage(t, f, "result=failure")
	require.Len(t, page.List, 1)
	require.Equal(t, 10002, *page.List[0].ErrorCode)
	require.NotEmpty(t, *page.List[0].ErrorMessage)
	require.NotContains(t, *page.List[0].ErrorMessage, "untrusted")
}
