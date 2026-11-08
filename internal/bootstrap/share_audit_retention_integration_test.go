package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSharePrivacyIntegrationAuditRetentionBoundaries(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	require.NoError(t, database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := database.WithContext(tx)
		defer cancel()
		var now time.Time
		require.NoError(t, q.Raw("SELECT CURRENT_TIMESTAMP").Scan(&now).Error)
		cutoff := now.UTC().AddDate(-3, 0, 0)
		for _, tc := range []struct{ action, resource string }{
			{"trade.share_link.create", "share_link"}, {"trade.share_link.regenerate", "share_link"}, {"trade.share_link.revoke", "share_link"},
			{"share.link.lifecycle.revoke", "share_link"}, {"share.visit.read", "share_link"},
			{"share.inquiry.submit", "inquiry"}, {"share.inquiry.update", "inquiry"}, {"share.inquiry.pii.read", "inquiry"},
			{"share.privacy.cleanup", "inquiry"}, {"share.privacy.cleanup", "customer"}, {"share.privacy.cleanup", "share_visit"},
			{"share.privacy.cleanup", "share_inquiry_receipt"}, {"share.privacy.cleanup", "audit_log"},
		} {
			for i, at := range []time.Time{cutoff.Add(-time.Microsecond), cutoff, cutoff.Add(time.Microsecond)} {
				id := insertShareAuditFixture(t, q, 1, tc.action, tc.resource, at)
				var due bool
				require.NoError(t, q.Raw("SELECT share_audit_retention_due(action,resource_type,created_at) FROM audit_log WHERE id=?", id).Scan(&due).Error)
				require.Equal(t, i < 2, due, tc.action)
				r := q.Exec("DELETE FROM audit_log WHERE id=? AND share_audit_retention_due(action,resource_type,created_at)", id)
				require.NoError(t, r.Error)
				if due {
					require.EqualValues(t, 1, r.RowsAffected)
				} else {
					require.Zero(t, r.RowsAffected)
				}
			}
		}
		return nil
	}))
	t.Log("all explicit share safety event/resource pairs: three-year cutoff inclusive; one microsecond fresh retained")
}

func TestSharePrivacyIntegrationAuditMutationGuards(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	old := time.Now().UTC().AddDate(-4, 0, 0)
	expired := insertShareAuditFixture(t, q, 1, "share.link.lifecycle.revoke", "share_link", old)
	fresh := insertShareAuditFixture(t, q, 1, "share.link.lifecycle.revoke", "share_link", time.Now().UTC())
	for _, tc := range []struct{ action, resource string }{
		{"identity.login", "user"}, {"trade.customer.update", "customer"}, {"share.future.unknown", "share_link"},
		{"trade.share_link.future", "share_link"}, {"share.visit.read", "sales_order"}, {"share.privacy.cleanup", "sales_order"},
	} {
		id := insertShareAuditFixture(t, q, 1, tc.action, tc.resource, old)
		requireShareAuditDenied(t, q, "DELETE FROM audit_log WHERE id=?", id)
	}
	requireShareAuditDenied(t, q, "DELETE FROM audit_log WHERE id=?", fresh)
	requireShareAuditDenied(t, q, "UPDATE audit_log SET created_at=? WHERE id=?", old, fresh)
	requireShareAuditDenied(t, q, "UPDATE audit_log SET result='failure' WHERE id=?", expired)
	requireShareAuditDenied(t, q, "TRUNCATE audit_log")
	requireShareAuditDenied(t, q, "DELETE FROM audit_log WHERE id IN (?,?)", expired, fresh)
	var remaining int64
	require.NoError(t, q.Table("audit_log").Where("id IN (?,?)", expired, fresh).Count(&remaining).Error)
	require.EqualValues(t, 2, remaining, "a mixed DELETE must roll back its eligible rows too")
	t.Log("recent/other-domain/unknown/mismatched events protected; UPDATE/TRUNCATE denied; mixed DELETE is atomic")
}

func TestSharePrivacyIntegrationAuditCleanupRollbackAndClockGuard(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	now := time.Now().UTC()
	expired := insertShareAuditFixture(t, q, 7, "share.inquiry.submit", "inquiry", now.AddDate(-4, 0, 0))
	fresh := insertShareAuditFixture(t, q, 7, "share.inquiry.submit", "inquiry", now.AddDate(-2, 0, 0))
	other := insertShareAuditFixture(t, q, 7, "order.pii.read", "sales_order", now.AddDate(-4, 0, 0))
	require.NoError(t, q.Exec(`CREATE FUNCTION reject_share_audit_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='share.privacy.cleanup' AND NEW.resource_type='audit_log' THEN RAISE EXCEPTION 'audit unavailable'; END IF; RETURN NEW; END $$`).Error)
	require.NoError(t, q.Exec("CREATE TRIGGER reject_share_audit_cleanup BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION reject_share_audit_cleanup()").Error)
	require.Error(t, cleanupSharePrivacy(ctx, database, now.AddDate(2, 0, 0)))
	var count int64
	require.NoError(t, q.Table("audit_log").Where("id=?", expired).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, q.Table("share_privacy_run").Where("tenant_id=7").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Exec("DROP TRIGGER reject_share_audit_cleanup ON audit_log").Error)
	require.NoError(t, cleanupSharePrivacy(ctx, database, now.AddDate(2, 0, 0)))
	require.NoError(t, q.Table("audit_log").Where("id=?", expired).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Table("audit_log").Where("id IN (?,?)", fresh, other).Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, q.Table("audit_log").Where("tenant_id=7 AND action='share.privacy.cleanup' AND resource_type='audit_log' AND result='success'").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, q.Table("share_privacy_run").Where("tenant_id=7 AND completed_at IS NOT NULL").Count(&count).Error)
	require.EqualValues(t, 1, count)
	t.Log("audit-only tenant cleaned; audit failure rolls deletion back; retry succeeds; future application clock cannot delete recent or other-domain audit")
}

func TestSharePrivacyIntegrationAuditMigrationDownRestoresAppendOnly(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	id := insertShareAuditFixture(t, q, 1, "share.inquiry.submit", "inquiry", time.Now().UTC().AddDate(-4, 0, 0))
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 13, state.Version)
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regprocedure('share_audit_retention_due(text,text,timestamptz)') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	requireShareAuditDenied(t, q, "DELETE FROM audit_log WHERE id=?", id)
	requireShareAuditDenied(t, q, "UPDATE audit_log SET result='failure' WHERE id=?", id)
	requireShareAuditDenied(t, q, "TRUNCATE audit_log")
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, latestBootstrapMigration(t), state.Version)
	require.False(t, state.Dirty)
	r := q.Exec("DELETE FROM audit_log WHERE id=?", id)
	require.NoError(t, r.Error)
	require.EqualValues(t, 1, r.RowsAffected)
	t.Log("14 -> 13 restores full append-only; 13 -> 14 restores narrowly bounded expiry; dirty=false")
}

func insertShareAuditFixture(t *testing.T, q *gorm.DB, tenant int64, action, resource string, at time.Time) int64 {
	t.Helper()
	var id int64
	require.NoError(t, q.Raw(`INSERT INTO audit_log(tenant_id,actor_type,action,resource_type,resource_id,trace_id,result,created_at)
		VALUES(?,'system',?,?,'retention','retention-boundary-fixture','success',?) RETURNING id`, tenant, action, resource, at).Scan(&id).Error)
	return id
}

func requireShareAuditDenied(t *testing.T, q *gorm.DB, query string, args ...any) {
	t.Helper()
	var pgErr *pgconn.PgError
	require.ErrorAs(t, q.Exec(query, args...).Error, &pgErr)
	require.Equal(t, "55000", pgErr.Code)
}
