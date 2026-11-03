package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestSyncIntegrationContinuationAndRestart(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 500))
	run := f.run(t)
	f.execute(t, run)
	partial := f.readRun(t, run.ID)
	require.Equal(t, "partial", partial.Status)
	require.Equal(t, 200, partial.Stats.Fetched)
	require.Equal(t, "200", f.cursor(t))
	rawCount(f.ctx, t, f.database, "listing", 200)
	rawCount(f.ctx, t, f.database, "raw_payload", 200)
	f.rebuild(t)
	f.execute(t, partial)
	partial = f.readRun(t, run.ID)
	require.Equal(t, 400, partial.Stats.Fetched)
	f.execute(t, partial)
	finished := f.readRun(t, run.ID)
	require.Equal(t, "success", finished.Status)
	require.Equal(t, 500, finished.Stats.Created)
	require.False(t, finished.Stats.Pending)
	require.Equal(t, "500", f.cursor(t))
	for _, table := range []string{"listing", "listing_sku", "raw_payload", "raw_dedup"} {
		rawCount(f.ctx, t, f.database, table, 500)
	}
	f.execute(t, run) // stale original event cannot run the next dispatch again.
	rawCount(f.ctx, t, f.database, "listing", 500)
	rawCount(f.ctx, t, f.database, "sync_run", 1)
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(NewSyncMetrics(f.ctx, f.database)))
	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 4)
	f.noLeaks(t, "application log", f.logs.text())
	f.noLeaks(t, "database", f.databaseSnapshot(t))
}

func TestSyncIntegrationThreeBadRecordsAdvance(t *testing.T) {
	items := syncDataset(t, 100)
	items[0] = json.RawMessage(`{"id":"product-001","price":1234,"currency":"CNY","status":"online","stock":10}`)
	items[1] = json.RawMessage(`{"id":"product-002","title":42,"price":1234,"currency":"CNY","status":"online","stock":10}`)
	items[2] = json.RawMessage(`{"id":"product-003","title":"mock product","price":1234,"currency":"CNY","status":"unrecognized","stock":10}`)
	f := newSyncFixture(t, items)
	run := f.run(t)
	f.execute(t, run)
	result := f.readRun(t, run.ID)
	require.Equal(t, "partial", result.Status)
	require.Equal(t, 97, result.Stats.Created)
	require.Equal(t, 3, result.Stats.Quarantined)
	require.False(t, result.Stats.Pending)
	require.Equal(t, "100", f.cursor(t))
	rawCount(f.ctx, t, f.database, "listing", 97)
	rawCount(f.ctx, t, f.database, "raw_payload", 100)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var reasons []string
	require.NoError(t, q.Table("quarantine_record").Select("reason_code").Scan(&reasons).Error)
	require.ElementsMatch(t, []string{"missing_required", "type_mismatch", "invalid_value"}, reasons)
}

func TestSyncIntegrationFetchFailureKeepsCursor(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 500))
	run := f.run(t)
	f.execute(t, run)
	f.platform.Fault = "500"
	f.execute(t, f.readRun(t, run.ID))
	failed := f.readRun(t, run.ID)
	require.Equal(t, "200", f.cursor(t))
	require.True(t, failed.Stats.Pending)
	rawCount(f.ctx, t, f.database, "listing", 200)
	require.Equal(t, 1, failed.Stats.Retries)
	f.platform.Fault = ""
	f.server.SetTime(f.now.Add(2 * time.Second))
	f.rebuild(t)
	f.execute(t, failed)
	f.execute(t, f.readRun(t, run.ID))
	require.Equal(t, "success", f.readRun(t, run.ID).Status)
	rawCount(f.ctx, t, f.database, "listing", 500)
}

func TestSyncIntegrationPageRollbackPreservesRaw(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 100))
	run := f.run(t)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION fail_listing() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.external_product_id='product-003' THEN RAISE EXCEPTION 'injected write failure'; END IF; RETURN NEW; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER fail_listing BEFORE INSERT ON listing FOR EACH ROW EXECUTE FUNCTION fail_listing()`))
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	rawCount(f.ctx, t, f.database, "listing", 0)
	rawCount(f.ctx, t, f.database, "sync_cursor", 0)
	rawCount(f.ctx, t, f.database, "raw_payload", 100)
	rawCount(f.ctx, t, f.database, "dead_letter", 0)
	require.Zero(t, f.readRun(t, run.ID).Stats.Fetched)
	require.NoError(t, f.database.Exec(f.ctx, `DROP TRIGGER fail_listing ON listing`))
	f.rebuild(t)
	f.execute(t, run)
	rawCount(f.ctx, t, f.database, "listing", 100)
	rawCount(f.ctx, t, f.database, "raw_payload", 100)
}
