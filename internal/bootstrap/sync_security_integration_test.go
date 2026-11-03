package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/kernel/scope"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func TestSyncIntegrationConcurrentStart(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.engine.StartRun(f.ctx, f.jobID, "manual"); errors <- err }()
	}
	wg.Wait()
	close(errors)
	ok, busy := 0, 0
	for err := range errors {
		if err == nil {
			ok++
		} else {
			require.Equal(t, 60201, errs.From(err).Code)
			busy++
		}
	}
	require.Equal(t, 1, ok)
	require.Equal(t, 1, busy)
	rawCount(f.ctx, t, f.database, "sync_run", 1)
	rawCount(f.ctx, t, f.database, "outbox_event", 1)
}

func TestSyncIntegrationCapabilityAndDispatchBoundaries(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	run := f.run(t)
	bare := context.Background()
	_, err := f.engineStore.LoadCursor(bare)
	require.Error(t, err)
	_, err = f.engineStore.CommitPage(bare, repo.Page{Run: run})
	require.Error(t, err)
	require.Error(t, f.engineStore.DeferRun(bare, run, "failed", 0, true))
	_, _, err = f.engineStore.LoadReplay(bare, run)
	require.Error(t, err)
	_, err = f.engineStore.ListRuns(bare, repo.PageQuery{Number: 1, Size: 20})
	require.Error(t, err)
	_, err = f.engineStore.ListRecords(bare, "dead_letter", repo.PageQuery{Number: 1, Size: 20})
	require.Error(t, err)
	_, err = f.engineStore.ReplaySource(bare, "dead_letter", 1)
	require.Error(t, err)
	for _, values := range [][3]int64{{2, f.shop.ID, run.ID}, {1, f.shop.ID + 1, run.ID}, {1, f.shop.ID, run.ID + 1}} {
		a, err := scope.IssueWorkAccess(scope.BootstrapIssuer{}, values[0], f.jobID, values[2], values[1], "product", f.mappingID, 1, []string{"sync.cursor"})
		require.NoError(t, err)
		_, err = f.engineStore.LoadCursor(scope.WithWorkAccess(bare, a))
		require.Error(t, err)
	}
	handle := syncEventHandler(f.engine, authorizationEventHandler(f.redis.cache))
	for _, payload := range []string{
		`{"run_id":1}`, `{"job_id":1,"run_id":1,"tenant_id":1}`,
		`{"job_id":1,"run_id":1,"shop_id":1}`, `{"job_id":1,"run_id":1,"scope":{}}`,
		`{"job_id":1,"run_id":1} {}`,
	} {
		require.Error(t, handle(f.ctx, outbox.Event{TenantID: 1, AggregateType: "sync_run", AggregateID: "1", EventType: "sync.dispatch", Payload: []byte(payload)}))
	}
	require.Error(t, f.engine.ExecuteDispatch(f.ctx, f.jobID, run.ID, 2, 0))
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID+1))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sync_job SET enabled=false WHERE id=?", f.jobID))
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sync_job SET enabled=true WHERE id=?", f.jobID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET deleted_at=now() WHERE id=?", f.shop.ID))
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	rawCount(f.ctx, t, f.database, "listing", 0)
	rawCount(f.ctx, t, f.database, "raw_payload", 0)
}

func TestSyncIntegrationPermissionsReleaseAndRevocation(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 300))
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop SET owner_user_id=? WHERE id=?", f.user.ID, f.shop.ID))
	operator := f.login(t)
	path := "/api/v1/admin/sync-jobs/" + itoa(f.jobID) + "/run"
	response, envelope := shopHTTPRequest(t, f.router, http.MethodPost, path, nil, operator.AccessToken)
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
	codes := []string{permission.SyncJobRun, permission.SyncRunList, permission.SyncRunRead, permission.SyncDLQList, permission.SyncDLQReplay, permission.SyncQuarantineList, permission.SyncQuarantineReplay}
	release := newPermissionReleaseService(t, f.adminFixture)
	result := applyRelease(t, f.adminFixture, release, codes, "m3b-sync")
	postInvalidation(t, f.adminFixture, result.AffectedUser)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodPost, path, nil, operator.AccessToken)
	require.Equal(t, 201, response.Code, response.Body.String())
	var run repo.Run
	require.NoError(t, json.Unmarshal(envelope.Data, &run))
	require.Equal(t, f.user.ID, *run.TriggeredBy)
	f.execute(t, run)
	require.Equal(t, "200", f.cursor(t))
	for _, path := range []string{"/sync-runs", "/sync-runs/" + itoa(run.ID), "/dead-letters", "/quarantines"} {
		response, _ := shopHTTPRequest(t, f.router, http.MethodGet, "/api/v1/admin"+path, nil, operator.AccessToken)
		require.Equal(t, 200, response.Code, response.Body.String())
	}
	role := operatorRole(t, f.adminFixture)
	ids := permissionIDs(t, f.adminFixture, permission.SyncJobRun)
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_role_permission WHERE role_id=? AND permission_id=?", role.ID, ids[0]))
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	stopped := f.readRun(t, run.ID)
	require.Equal(t, "cancelled", stopped.Status)
	require.False(t, stopped.Stats.Pending)
	require.Equal(t, "200", f.cursor(t))
	rawCount(f.ctx, t, f.database, "listing", 200)
	result = applyRelease(t, f.adminFixture, release, codes, "m3b-sync")
	require.False(t, result.Changed)
	response, envelope = shopHTTPRequest(t, f.router, http.MethodPost, path, nil, operator.AccessToken)
	require.Equal(t, 403, response.Code)
	require.Equal(t, 20107, envelope.Code)
}
