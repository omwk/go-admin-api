package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/stretchr/testify/require"
)

func replayFixture(t *testing.T, count int) *syncFixture {
	t.Helper()
	f := newSyncFixture(t, nil)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, syncmodule.RegisterReplayRoutes(f.router, f.identityFixture.service, profiles, f.engineStore))
	if count > 0 {
		seedReplayRaw(t, f, count)
	}
	return f
}

func seedReplayRaw(t *testing.T, f *syncFixture, count int) []ingestion.RawRef {
	t.Helper()
	dataset := syncDataset(t, count)
	items := make([]ingestion.RawItem, count)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	for i := range items {
		items[i] = ingestion.RawItem{ExternalID: fmt.Sprintf("product-%03d", i+1), Payload: dataset[i], FetchedAt: stamp.Add(time.Duration(i%3) * time.Microsecond)}
	}
	refs, err := rawServiceFor(t, f.database, 1, f.shop.PlatformID).SaveRaw(f.ctx, f.shop.ID, "product", items)
	require.NoError(t, err)
	return refs
}

func replayRequest(f *syncFixture) syncmodule.ReplayRequest {
	from, to := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	return syncmodule.ReplayRequest{ShopID: f.shop.ID, Resource: "product", MappingVersionID: f.mappingID, Scope: syncmodule.ReplayScope{Type: "time", From: &from, To: &to}}
}

func createReplay(t *testing.T, f *syncFixture) syncmodule.ReplayRun {
	t.Helper()
	run, err := f.engineStore.CreateReplay(f.ctx, replayRequest(f), nil)
	require.NoError(t, err)
	return run
}

func TestReplayIntegrationFrozenMembersRestartAndAtomicRollback(t *testing.T) {
	// REQ-SYNC-011: 500 frozen members survive process reconstruction and rollback.
	f := replayFixture(t, 500)
	run := createReplay(t, f)
	require.EqualValues(t, 500, run.Total)
	raw := ingestion.RawItem{ExternalID: "late", Payload: []byte(`{"id":"late"}`)}
	_, err := rawServiceFor(t, f.database, 1, f.shop.PlatformID).SaveRaw(f.ctx, f.shop.ID, "product", []ingestion.RawItem{raw})
	require.NoError(t, err)
	crashAfterFirstReplayBatch(t, f, run.ID)
	run, err = f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.EqualValues(t, 200, run.Processed)
	checkpoint := string(run.CheckpointKey)
	rawCount(f.ctx, t, f.database, "listing", 200)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_replay_stats() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.processed > 200 THEN RAISE EXCEPTION 'injected stats failure'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER fail_replay_stats BEFORE UPDATE ON replay_run FOR EACH ROW EXECUTE FUNCTION reject_replay_stats()`))
	_, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 200)
	require.Error(t, err)
	rawCount(f.ctx, t, f.database, "listing", 200)
	rawCount(f.ctx, t, f.database, "listing_sku", 200)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 200)
	persisted, err := f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.EqualValues(t, 200, persisted.Processed)
	require.JSONEq(t, checkpoint, string(persisted.CheckpointKey))
	require.NoError(t, f.database.Exec(f.ctx, "DROP TRIGGER fail_replay_stats ON replay_run"))
	f.rebuild(t) // All execution state is reconstructed from the database.
	for _, before := range []int64{200, 400} {
		run, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, before)
		require.NoError(t, err)
	}
	require.Equal(t, "success", run.Status)
	require.EqualValues(t, 500, run.Total)
	require.EqualValues(t, 500, run.Processed)
	require.Equal(t, run.Processed, run.Stats.Processed())
	require.EqualValues(t, 500, run.Stats.Inserted)
	rawCount(f.ctx, t, f.database, "listing", 500)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 500)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("replay_run_item").Where("run_id=? AND outcome='inserted' AND disposed_at IS NOT NULL", run.ID).Count(&count).Error)
	require.EqualValues(t, 500, count)
	require.NoError(t, q.Table("replay_run_item").Where("run_id=? AND external_id='late'", run.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, q.Table("sync_cursor").Count(&count).Error)
	require.Zero(t, count)
	_, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 200)
	require.NoError(t, err)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 500)
}

func TestReplayIntegrationCreateAPIEmptyAndArchived(t *testing.T) {
	f := replayFixture(t, 0)
	response, envelope := f.request(t, "POST", "/replays", replayRequest(f))
	require.Equal(t, 201, response.Code, response.Body.String())
	var run syncmodule.ReplayRun
	require.NoError(t, json.Unmarshal(envelope.Data, &run))
	require.Zero(t, run.Total)
	require.Equal(t, "success", run.Status)
	require.NotNil(t, run.FinishedAt)
	response, _ = f.request(t, "GET", "/replays/"+itoa(run.ID), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	rawCount(f.ctx, t, f.database, "replay_run_item", 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events int64
	require.NoError(t, q.Table("outbox_event").Where("event_type='replay.dispatch'").Count(&events).Error)
	require.Zero(t, events)
	refs := seedReplayRaw(t, f, 1)
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM raw_payload WHERE id=? AND fetched_at=?", refs[0].ID, refs[0].FetchedAt))
	_, err := f.engineStore.CreateReplay(f.ctx, replayRequest(f), nil)
	require.Equal(t, 60601, errs.From(err).Code)
	rawCount(f.ctx, t, f.database, "replay_run", 1)
	request := replayRequest(f)
	request.Scope.Type = "archive"
	_, err = f.engineStore.CreateReplay(f.ctx, request, nil)
	require.Equal(t, 60601, errs.From(err).Code)
}

func TestReplayIntegrationMissingRawRetainsCheckpoint(t *testing.T) {
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM raw_payload WHERE shop_id=?", f.shop.ID))
	_, err := f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.ErrorContains(t, err, "checkpoint retained")
	persisted, err := f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.Zero(t, persisted.Processed)
	require.Nil(t, persisted.FinishedAt)
	rawCount(f.ctx, t, f.database, "listing", 0)
}

func TestReplayIntegrationOverLimitRollsBackFreeze(t *testing.T) {
	f := replayFixture(t, 0)
	for from := 1; from <= 500001; from += 10000 {
		fresh, stop := f.database.WithContext(f.ctx)
		require.NoError(t, fresh.Exec(`INSERT INTO raw_dedup(tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,raw_id,fetched_at)
 SELECT 1,?,?,'product','limit-'||n,n::text,n,now() FROM generate_series(?::bigint,?::bigint) n`, f.shop.PlatformID, f.shop.ID, from, min(from+9999, 500001)).Error)
		stop()
	}
	started := time.Now()
	_, err := f.engineStore.CreateReplay(f.ctx, replayRequest(f), nil)
	require.Equal(t, 60601, errs.From(err).Code)
	rawCount(f.ctx, t, f.database, "replay_run", 0)
	rawCount(f.ctx, t, f.database, "replay_run_item", 0)
	t.Logf("500001 freeze rejection and rollback: %s", time.Since(started))
}

// A canceled task must never commit a partial member list.
func TestReplayIntegrationCanceledFreeze(t *testing.T) {
	f := replayFixture(t, 1)
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	_, err := f.engineStore.CreateReplay(ctx, replayRequest(f), nil)
	require.Error(t, err)
	rawCount(f.ctx, t, f.database, "replay_run", 0)
}
