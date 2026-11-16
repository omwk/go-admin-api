package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantPurgeIntegrationFailureRollsBack(t *testing.T) {
	// BE-IDN-011 §4.8: fail after grants, tokens, notifications, dictionaries were deleted.
	f := newTenantFixture(t)
	result := seedTenantPurge(t, f, "purge-rollback")
	purgeGrace(t, f, 0)
	archiveTenantForPurge(t, f, result.TenantID)
	before := tenantPurgeCounts(t, f, result.TenantID)
	fingerprints := tenantPurgeFingerprint(t, f, result.TenantID)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION fail_tenant_purge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'injected tenant purge failure'; END; $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER fail_tenant_purge BEFORE DELETE ON sys_user FOR EACH STATEMENT EXECUTE FUNCTION fail_tenant_purge()`))
	w, _ := f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm=purge-rollback", nil, "failed-purge")
	require.Equal(t, 500, w.Code)
	require.NotContains(t, w.Body.String(), "injected")
	after := tenantPurgeCounts(t, f, result.TenantID)
	require.Equal(t, before, after)
	require.Equal(t, fingerprints, tenantPurgeFingerprint(t, f, result.TenantID))
	assertTenantStatus(t, f, result.TenantID, "suspended")
	assertNoTenantPurgeAudit(t, f)
	logTenantPurgeCounts(t, "rollback_before", before)
	logTenantPurgeCounts(t, "rollback_after", after)
	t.Log("injected sys_user DELETE failure=500; all rows and contents unchanged; tenant remains; purge audit/idempotency absent")
}

func TestIdentityTenantPurgeIntegrationAuditFailureRollsBack(t *testing.T) {
	f := newTenantFixture(t)
	result := seedTenantPurge(t, f, "purge-audit-rollback")
	purgeGrace(t, f, 0)
	archiveTenantForPurge(t, f, result.TenantID)
	before := tenantPurgeFingerprint(t, f, result.TenantID)
	failing, err := identity.NewAdminService(newAdminStore(f.database), func(context.Context, audit.Event) error {
		return errors.New("injected audit failure")
	})
	require.NoError(t, err)
	failing.UseSettings(&systemSettingStore{database: f.database})
	sealer, err := crypto.NewKeyring(map[int][]byte{1: f.secret}, 1)
	require.NoError(t, err)
	service, err := identity.NewTenantService(newTenantStore(f.database), failing, 1, "", func() time.Time { return f.now }, sealer)
	require.NoError(t, err)
	f.router = gin.New()
	require.NoError(t, identity.RegisterTenantRoutes(f.router, f.identityFixture.service, f.profiles, service))
	w, _ := f.request(t, "DELETE", tenantPath(result.TenantID)+"?confirm=purge-audit-rollback", nil, "failed-purge")
	require.Equal(t, 500, w.Code)
	require.Equal(t, before, tenantPurgeFingerprint(t, f, result.TenantID))
	assertNoTenantPurgeAudit(t, f)
	t.Log("audit failure after all DELETE statements=500; tenant and all table fingerprints restored; no purge audit or saved response")
}

func assertNoTenantPurgeAudit(t *testing.T, f *tenantFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("audit_log").Where("action='system.tenant.purge'").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Table("idempotency_key").Where("tenant_id=1 AND scope LIKE '%DELETE:/api/v1/admin/tenants/%'").Count(&count).Error)
	require.Zero(t, count)
}
