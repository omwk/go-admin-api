package bootstrap

import (
	"encoding/json"
	"github.com/omwk/go-admin-api/internal/modules/shop"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func replaceSyncCredential(t *testing.T, f *syncFixture) (int64, int64) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("shop_credential").Select("id").Where("shop_id=?", f.shop.ID).Take(&id).Error)
	value, err := json.Marshal(f.randomValue(t))
	require.NoError(t, err)
	expires := time.Now().UTC().Add(time.Hour)
	_, err = f.credentialFixture.service.RotateCredential(f.ctx, f.actor, id, shop.CredentialRotateRequest{Values: map[string]json.RawMessage{"private_value": value}, ExpiresAt: &expires})
	require.NoError(t, err)
	var version int64
	require.NoError(t, q.Table("shop_credential").Select("current_version_id").Where("id=?", id).Take(&version).Error)
	return id, version
}

func TestSyncIntegrationCredentialBlockTracksUsedCredential(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET expires_at=now()-interval '1 minute' WHERE shop_id=?", f.shop.ID))
	used, _ := f.create(t, "oauth2")
	f.platform.Fault = "auth_expired"
	run := f.run(t)
	f.execute(t, run)
	require.Equal(t, "credential_expired", f.readRun(t, run.ID).ErrorCode)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var blocked int64
	require.NoError(t, q.Table("sync_job").Select("credential_blocked_id").Where("id=?", f.jobID).Take(&blocked).Error)
	require.Equal(t, used.ID, blocked)
}

func TestSyncIntegrationRefreshingKeepsConfirmedUnexpiredAccess(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET status='refreshing' WHERE shop_id=?", f.shop.ID))
	run := f.run(t)
	f.execute(t, run)
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
}

// REQ-SHP-008: credential maintenance resumes only the block it owns.
func TestSyncIntegrationCredentialBlockOwnershipAndAdministratorDisable(t *testing.T) {
	for _, adminDisabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "credential_blocked", true: "admin_disabled"}[adminDisabled], func(t *testing.T) {
			f := newSyncFixture(t, syncDataset(t, 1))
			p := observeSyncPlatform(t, f)
			require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET expires_at=now()-interval '1 minute' WHERE shop_id=?", f.shop.ID))
			run := f.run(t)
			f.execute(t, run)
			current := f.readRun(t, run.ID)
			require.True(t, current.Stats.Pending)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var state struct {
				Enabled             bool
				BlockReason         string
				CredentialBlockedID *int64
			}
			require.NoError(t, q.Table("sync_job").Select("enabled,block_reason,credential_blocked_id").Where("id=?", f.jobID).Take(&state).Error)
			require.True(t, state.Enabled)
			require.Equal(t, "credential_blocked", state.BlockReason)
			require.NotNil(t, state.CredentialBlockedID)
			if adminDisabled {
				require.NoError(t, f.database.Exec(f.ctx, "UPDATE sync_job SET enabled=false WHERE id=?", f.jobID))
			}
			id, version := replaceSyncCredential(t, f)
			require.NoError(t, recoverCredentialJobs(f.ctx, f.database, 1, id, version))
			require.NoError(t, q.Table("sync_job").Select("enabled,block_reason,credential_blocked_id").Where("id=?", f.jobID).Take(&state).Error)
			if adminDisabled {
				require.False(t, state.Enabled)
				require.Equal(t, "admin_disabled", state.BlockReason)
				require.NotNil(t, state.CredentialBlockedID)
				require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
				require.Zero(t, p.calls)
				return
			}
			require.True(t, state.Enabled)
			require.Empty(t, state.BlockReason)
			require.Nil(t, state.CredentialBlockedID)
			awakened := f.readRun(t, run.ID)
			require.NoError(t, recoverCredentialJobs(f.ctx, f.database, 1, id, version))
			require.Equal(t, awakened.Stats.Dispatch, f.readRun(t, run.ID).Stats.Dispatch, "duplicate event must not dispatch twice")
			f.execute(t, awakened)
			require.Equal(t, "success", f.readRun(t, run.ID).Status)
			require.Equal(t, 1, p.calls)
		})
	}
}

func TestSyncIntegrationCredentialRecoveryKeepsCommittedCheckpoint(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 201))
	p := observeSyncPlatform(t, f)
	p.fetch = func() {
		if p.calls == 1 {
			require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET expires_at=now()-interval '1 minute' WHERE shop_id=?", f.shop.ID))
		}
	}
	run := f.run(t)
	f.execute(t, run)
	current := f.readRun(t, run.ID)
	if current.ErrorCode != "credential_expired" {
		f.execute(t, current)
		current = f.readRun(t, run.ID)
	}
	require.Equal(t, "credential_expired", current.ErrorCode)
	require.Equal(t, 1, p.calls)
	require.Equal(t, "100", f.cursor(t))
	require.Equal(t, 100, current.Stats.Fetched)
	rawCount(f.ctx, t, f.database, "listing", 100)
	id, version := replaceSyncCredential(t, f)
	require.NoError(t, recoverCredentialJobs(f.ctx, f.database, 1, id, version))
	require.Equal(t, "100", f.cursor(t))
	resumed := f.readRun(t, run.ID)
	require.Equal(t, 100, resumed.Stats.Fetched)
	f.execute(t, resumed)
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
	require.Equal(t, 3, p.calls)
	require.Equal(t, "201", f.cursor(t))
	rawCount(f.ctx, t, f.database, "listing", 201)
	rawCount(f.ctx, t, f.database, "raw_payload", 201)
}

func TestSyncIntegrationCredentialRecoveryPreservesOrderDetailPending(t *testing.T) {
	f, p := newOrderSyncFixture(t, 3)
	p.failures["order-01"] = true
	run := f.run(t)
	f.execute(t, run)
	current := f.readRun(t, run.ID)
	require.Equal(t, 2, current.Stats.OrderCompleted)
	require.Equal(t, 1, current.Stats.OrderPending)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE shop_credential SET expires_at=now()-interval '1 minute' WHERE shop_id=?", f.shop.ID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE order_sync_pending SET next_retry_at=now()-interval '1 minute' WHERE shop_id=?", f.shop.ID))
	f.execute(t, current)
	blocked := f.readRun(t, run.ID)
	require.Equal(t, "credential_expired", blocked.ErrorCode)
	require.EqualValues(t, 1, orderTableCount(t, f, "order_sync_pending", "status='pending'"))
	p.failures = map[string]bool{}
	id, version := replaceSyncCredential(t, f)
	require.NoError(t, recoverCredentialJobs(f.ctx, f.database, 1, id, version))
	f.execute(t, f.readRun(t, run.ID))
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
	require.Equal(t, 1, p.calls["order-00"])
	require.Equal(t, 1, p.calls["order-02"])
	require.EqualValues(t, 3, orderTableCount(t, f, "sales_order", "true"))
	require.Zero(t, orderTableCount(t, f, "order_sync_pending", "status<>'done'"))
}
