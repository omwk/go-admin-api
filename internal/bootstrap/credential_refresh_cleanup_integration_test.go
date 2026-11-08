package bootstrap

import (
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/refresh"
	"github.com/stretchr/testify/require"
)

func TestCredentialRefreshIntegrationStagingCleanupIsExplicitAndIdempotent(t *testing.T) {
	// REQ-SHP-008: clearing the sole uncommitted result first records its loss.
	f := newCredentialRefreshFixture(t)
	f.executor = f.newExecutor(t, &credentialRefreshFaults{Store: f.refreshStore, failPublish: true})
	run := f.prepare(t)
	run, err := f.executor.Execute(f.ctx, f.target, run.ID)
	require.Error(t, err)
	require.Equal(t, refresh.PhaseConsumed, run.Phase)
	before, err := f.refreshStore.Cleanup(f.ctx, 1, f.now.Add(24*time.Hour-time.Microsecond), 100)
	require.NoError(t, err)
	require.Equal(t, refresh.CleanupResult{}, before)
	at := f.now.Add(24 * time.Hour)
	cleaned, err := f.refreshStore.Cleanup(f.ctx, 1, at, 100)
	require.NoError(t, err)
	require.Equal(t, 1, cleaned.Stages)
	require.Zero(t, cleaned.Versions)
	repeated, err := f.refreshStore.Cleanup(f.ctx, 1, at, 100)
	require.NoError(t, err)
	require.Equal(t, refresh.CleanupResult{}, repeated)
	stored := f.storedRun(t)
	require.Equal(t, refresh.PhaseUnknown, stored.Phase)
	require.Equal(t, "stage_expired", stored.Reason)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stage model.RefreshStage
	require.NoError(t, q.Select(refreshStageColumns).Where("id=?", *run.StagedResultID).Take(&stage).Error)
	require.Empty(t, stage.CipherText)
	require.Empty(t, stage.Nonce)
	require.NotNil(t, stage.PurgedAt)
	row, current := f.readStored(t, f.target.CredentialID, 1)
	require.Equal(t, current.ID, *row.CurrentVersionID)
	require.NotEmpty(t, current.CipherText)
	var pending int64
	require.NoError(t, q.Table("credential_refresh_reconciliation").Where("run_id=? AND resolved_at IS NULL", run.ID).Count(&pending).Error)
	require.EqualValues(t, 1, pending)
	f.executor = f.newExecutor(t, f.refreshStore)
	f.now = at
	_, err = f.executor.Recover(f.ctx, f.target, run.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), f.platform.calls.Load())
	f.assertSafe(t)
}

func TestCredentialRefreshIntegrationPublishedHistoryRetentionAndPurgeAudit(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	run := f.prepare(t)
	run, err := f.executor.Execute(f.ctx, f.target, run.ID)
	require.NoError(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stage model.RefreshStage
	require.NoError(t, q.Select(refreshStageColumns).Where("id=?", *run.StagedResultID).Take(&stage).Error)
	require.Empty(t, stage.CipherText, "published transaction must remove its temporary copy")
	_, old := f.readStored(t, f.target.CredentialID, 1)
	before := old.PurgeAfter.Add(-time.Microsecond)
	cleaned, err := f.refreshStore.Cleanup(f.ctx, 1, before, 100)
	require.NoError(t, err)
	require.Zero(t, cleaned.Versions)
	_, old = f.readStored(t, f.target.CredentialID, 1)
	require.NotEmpty(t, old.CipherText)
	cleaned, err = f.refreshStore.Cleanup(f.ctx, 1, *old.PurgeAfter, 100)
	require.NoError(t, err)
	require.Equal(t, 1, cleaned.Versions)
	_, old = f.readStored(t, f.target.CredentialID, 1)
	row, current := f.readStored(t, f.target.CredentialID, 2)
	require.Empty(t, old.CipherText)
	require.Empty(t, old.Nonce)
	require.NotNil(t, old.RetiredAt)
	require.Equal(t, current.ID, *row.CurrentVersionID)
	require.NotEmpty(t, current.CipherText)
	again, err := f.refreshStore.Cleanup(f.ctx, 1, *old.PurgeAfter, 100)
	require.NoError(t, err)
	require.Equal(t, refresh.CleanupResult{}, again)
	var purges, versions int64
	require.NoError(t, q.Model(&model.CredentialAudit{}).Where("action='purge'").Count(&purges).Error)
	require.NoError(t, q.Model(&model.CredentialVersion{}).Count(&versions).Error)
	require.EqualValues(t, 1, purges)
	require.EqualValues(t, 2, versions)
	refs, err := f.refreshStore.KeyReferences(f.ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []refresh.KeyReference{{KeyVersion: 1, Versions: 1}}, refs)
}

func TestCredentialRefreshIntegrationCleanupAuditFailureRollsBack(t *testing.T) {
	f := newCredentialRefreshFixture(t)
	f.executor = f.newExecutor(t, &credentialRefreshFaults{Store: f.refreshStore, failPublish: true})
	run := f.prepare(t)
	run, err := f.executor.Execute(f.ctx, f.target, run.ID)
	require.Error(t, err)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec(`CREATE FUNCTION reject_refresh_purge() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN IF NEW.action='purge' THEN RAISE EXCEPTION 'injected purge audit failure'; END IF; RETURN NEW; END $$`).Error)
	require.NoError(t, q.Exec(`CREATE TRIGGER reject_refresh_purge BEFORE INSERT ON credential_audit
        FOR EACH ROW EXECUTE FUNCTION reject_refresh_purge()`).Error)
	_, err = f.refreshStore.Cleanup(f.ctx, 1, f.now.Add(24*time.Hour), 100)
	require.Error(t, err)
	require.Equal(t, refresh.PhaseConsumed, f.storedRun(t).Phase)
	var stage model.RefreshStage
	require.NoError(t, q.Select(refreshStageColumns).Where("id=?", *run.StagedResultID).Take(&stage).Error)
	require.NotEmpty(t, stage.CipherText)
	require.Nil(t, stage.PurgedAt)
	require.NoError(t, q.Exec("DROP TRIGGER reject_refresh_purge ON credential_audit").Error)
	cleaned, err := f.refreshStore.Cleanup(f.ctx, 1, f.now.Add(24*time.Hour), 100)
	require.NoError(t, err)
	require.Equal(t, 1, cleaned.Stages)
}
