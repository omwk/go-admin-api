package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func driftCandidate(t *testing.T, f *syncFixture) (schemaBaseline, schemaBaseline, repo.DriftEvent) {
	t.Helper()
	active := warmSchema(t, f)
	f.platform.Fault = "drift"
	require.Equal(t, "success", completeCycle(t, f).Status)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var candidate schemaBaseline
	var event repo.DriftEvent
	require.NoError(t, q.Table("schema_baseline").Where("status='superseded'").Order("version DESC").Take(&candidate).Error)
	require.NoError(t, q.Table("schema_drift_event").Take(&event).Error)
	return active, candidate, event
}

func TestSyncDriftIntegrationAdminReleaseAndActions(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	_, candidate, event := driftCandidate(t, f)
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET owner_user_id=? WHERE id=?", f.user.ID, f.shop.ID))
	operator := f.login(t)
	paths := []string{"/schema-drifts", "/schema-drifts/" + itoa(event.ID) + "/ack", "/schema-drifts/" + itoa(event.ID) + "/resolve", "/schema-drifts/" + itoa(event.ID) + "/ignore", "/schema-baselines/" + itoa(candidate.ID) + "/promote"}
	for i, path := range paths {
		method := http.MethodPost
		if i == 0 {
			method = http.MethodGet
		}
		response, envelope := shopHTTPRequest(t, f.router, method, "/api/v1/admin"+path, nil, operator.AccessToken)
		require.Equal(t, 403, response.Code)
		require.Equal(t, 20107, envelope.Code)
	}
	codes := []string{permission.SyncDriftList, permission.SyncDriftAck, permission.SyncDriftResolve, permission.SyncDriftIgnore, permission.SyncBaselinePromote}
	release := newPermissionReleaseService(t, f.adminFixture)
	result := applyRelease(t, f.adminFixture, release, codes, "m3b-drift")
	postInvalidation(t, f.adminFixture, result.AffectedUser)
	for i, path := range paths {
		method := http.MethodPost
		if i == 0 {
			method = http.MethodGet
		}
		response, _ := shopHTTPRequest(t, f.router, method, "/api/v1/admin"+path, nil, operator.AccessToken)
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var status string
	require.NoError(t, q.Table("schema_drift_event").Select("status").Where("id=?", event.ID).Take(&status).Error)
	require.Equal(t, "ignored", status)
	role := operatorRole(t, f.adminFixture)
	ids := permissionIDs(t, f.adminFixture, permission.SyncDriftAck)
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_role_permission WHERE role_id=? AND permission_id=?", role.ID, ids[0]))
	result = applyRelease(t, f.adminFixture, release, codes, "m3b-drift")
	require.False(t, result.Changed)
	response, envelope := shopHTTPRequest(t, f.router, http.MethodPost, "/api/v1/admin"+paths[1], nil, operator.AccessToken)
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
	// Ignore persists as a path whitelist beyond the rolling dedup window.
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE schema_drift_event SET detected_at=now()-interval '25 hours'"))
	completeCycle(t, f)
	rawCount(f.ctx, t, f.database, "schema_drift_event", 1)
}

func TestSyncDriftIntegrationPromoteAuditRollbackAndIdempotency(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	active, candidate, event := driftCandidate(t, f)
	ctx := logger.WithTraceID(f.ctx, "drift-admin-trace")
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION fail_schema_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'sync:%' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER fail_schema_audit BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION fail_schema_audit()`))
	require.Error(t, f.engineStore.PromoteBaseline(ctx, candidate.ID, nil))
	require.Error(t, f.engineStore.ChangeDrift(ctx, event.ID, "resolved", nil))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var current schemaBaseline
	require.NoError(t, q.Table("schema_baseline").Where("status='active'").Take(&current).Error)
	require.Equal(t, active.ID, current.ID)
	var status string
	require.NoError(t, q.Table("schema_drift_event").Select("status").Where("id=?", event.ID).Take(&status).Error)
	require.Equal(t, "open", status)
	require.NoError(t, f.database.Exec(f.ctx, "DROP TRIGGER fail_schema_audit ON audit_log"))
	path := "/api/v1/admin/schema-baselines/" + itoa(candidate.ID) + "/promote"
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer "+f.bearer)
		request.Header.Set("Idempotency-Key", "promote-repeat")
		response := httptest.NewRecorder()
		f.router.ServeHTTP(response, request)
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	current = schemaBaseline{}
	require.NoError(t, q.Table("schema_baseline").Where("status='active'").Take(&current).Error)
	require.Equal(t, candidate.ID, current.ID)
	require.Contains(t, current.Fields, "$.drift_field")
	require.NoError(t, q.Table("schema_baseline").Select("status").Where("id=?", active.ID).Take(&status).Error)
	require.Equal(t, "superseded", status)
	var audits int64
	require.NoError(t, q.Table("audit_log").Where("action='sync:baseline:promote' AND resource_id=?", itoa(candidate.ID)).Count(&audits).Error)
	require.EqualValues(t, 1, audits)
}

func TestSyncDriftIntegrationAdminScopeDenials(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	_, candidate, event := driftCandidate(t, f)
	for _, ctx := range []context.Context{
		scope.WithAccess(f.ctx, scope.Access{}),
		scope.WithAccess(f.ctx, scope.Access{Tenant: 2, User: f.actor.UserID, Roles: []scope.Role{{Kind: "all"}}}),
		scope.WithAccess(f.ctx, scope.Access{Tenant: 1, User: f.user.ID, Roles: []scope.Role{{Kind: "self"}}}),
	} {
		result, err := f.engineStore.ListDrifts(ctx, repo.PageQuery{Number: 1, Size: 20})
		if err == nil {
			require.Zero(t, result.Total)
		}
		require.Error(t, f.engineStore.ChangeDrift(ctx, event.ID, "acked", nil))
		require.Error(t, f.engineStore.PromoteBaseline(ctx, candidate.ID, nil))
	}
}
