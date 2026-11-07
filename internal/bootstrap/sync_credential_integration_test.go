package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/connector"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

type observedSyncPlatform struct {
	connector.Platform
	fetch func()
	calls int
}

func (p *observedSyncPlatform) Fetch(ctx context.Context, c connector.Credential, req connector.FetchRequest) (connector.FetchResult, error) {
	p.calls++
	result, err := p.Platform.Fetch(ctx, c, req)
	if p.fetch != nil {
		p.fetch()
	}
	return result, err
}

func observeSyncPlatform(t *testing.T, f *syncFixture) *observedSyncPlatform {
	t.Helper()
	p := &observedSyncPlatform{Platform: f.platform}
	r := connector.NewRegistry()
	require.NoError(t, r.Register(p))
	var err error
	f.engine, f.engineStore, err = buildSyncRuntime(f.database, f.redis.cache, f.credentialFixture.service, r, testenv.Timeouts(t).Sync)
	require.NoError(t, err)
	return p
}

func TestSyncIntegrationCredentialAudit(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	p := observeSyncPlatform(t, f)
	run := f.run(t)
	f.execute(t, run)
	require.Equal(t, 1, p.calls)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var audit struct {
		ActorType string
		Detail    json.RawMessage
	}
	require.NoError(t, q.Table("credential_audit").Select("actor_type,detail").Where("actor_type='system'").Take(&audit).Error)
	require.Equal(t, "system", audit.ActorType)
	var detail map[string]any
	require.NoError(t, json.Unmarshal(audit.Detail, &detail))
	require.Equal(t, float64(f.jobID), detail["job_id"])
	require.Equal(t, float64(run.ID), detail["run_id"])
	require.Equal(t, false, detail["metadata_only"])
	f.noLeaks(t, "credential audit", string(audit.Detail))
	f.noLeaks(t, "database", f.databaseSnapshot(t))
	f.noLeaks(t, "application log", f.logs.text())
	f.noLeaks(t, "SQL log", f.sqlLogs.text())
}

func TestSyncIntegrationCredentialAuditFailureAndExpiry(t *testing.T) {
	for _, mode := range []string{"audit_failure", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newSyncFixture(t, syncDataset(t, 1))
			p := observeSyncPlatform(t, f)
			run := f.run(t)
			if mode == "audit_failure" {
				require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION fail_worker_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.actor_type='system' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`))
				require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER fail_worker_audit BEFORE INSERT ON credential_audit FOR EACH ROW EXECUTE FUNCTION fail_worker_audit()`))
			} else {
				require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET expires_at=now()-interval '1 minute' WHERE shop_id=?", f.shop.ID))
			}
			f.execute(t, run)
			require.Zero(t, p.calls)
			rawCount(f.ctx, t, f.database, "raw_payload", 0)
			rawCount(f.ctx, t, f.database, "listing", 0)
			result := f.readRun(t, run.ID)
			if mode == "expired" {
				require.Equal(t, "failed", result.Status)
				require.False(t, result.Stats.Pending)
				require.Equal(t, "credential_expired", result.ErrorCode)
				_, err := f.engineStore.ResolveJob(f.ctx, f.jobID)
				require.Error(t, err)
			} else {
				require.True(t, result.Stats.Pending)
				require.Equal(t, "fetch_unavailable", result.ErrorCode)
			}
		})
	}
}

func TestSyncIntegrationRevocationDuringFetch(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET owner_user_id=? WHERE id=?", f.user.ID, f.shop.ID))
	release := newPermissionReleaseService(t, f.adminFixture)
	applyRelease(t, f.adminFixture, release, []string{permission.SyncJobRun}, "m3b-fetch-revocation")
	ctx, err := f.engineStore.Authorize(f.ctx, 1, f.user.ID, permission.SyncJobRun)
	require.NoError(t, err)
	p := observeSyncPlatform(t, f)
	run, err := f.engine.StartRun(ctx, f.jobID, "manual")
	require.NoError(t, err)
	p.fetch = func() {
		role := operatorRole(t, f.adminFixture)
		ids := permissionIDs(t, f.adminFixture, permission.SyncJobRun)
		require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_role_permission WHERE role_id=? AND permission_id=?", role.ID, ids[0]))
	}
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	require.Equal(t, 1, p.calls)
	rawCount(f.ctx, t, f.database, "raw_payload", 0)
	rawCount(f.ctx, t, f.database, "listing", 0)
	rawCount(f.ctx, t, f.database, "sync_cursor", 0)
	require.Equal(t, "cancelled", f.readRun(t, run.ID).Status)
}
