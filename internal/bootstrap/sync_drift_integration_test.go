package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/mock"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func completeCycle(t *testing.T, f *syncFixture) repo.Run {
	t.Helper()
	run := f.run(t)
	for i := 0; i < 10; i++ {
		f.server.SetTime(f.now.Add(time.Duration(run.ID*100+int64(i)*10) * time.Second))
		f.execute(t, run)
		run = f.readRun(t, run.ID)
		if !run.Stats.Pending {
			return run
		}
	}
	t.Fatal("cycle did not finish")
	return repo.Run{}
}

func warmSchema(t *testing.T, f *syncFixture) schemaBaseline {
	t.Helper()
	for i := 0; i < 3; i++ {
		run := completeCycle(t, f)
		require.Equal(t, "success", run.Status)
		if i < 2 {
			rawCount(f.ctx, t, f.database, "schema_baseline", 0)
		}
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var active schemaBaseline
	require.NoError(t, q.Table("schema_baseline").Where("status='active'").Take(&active).Error)
	return active
}

func TestSyncDriftIntegrationWarmupContinuationAndPageDetection(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 500))
	for cycle := 0; cycle < 3; cycle++ {
		run := f.run(t)
		f.server.SetTime(f.now.Add(time.Duration(cycle+1) * 100 * time.Second))
		f.execute(t, run)
		partial := f.readRun(t, run.ID)
		require.Equal(t, 200, partial.Stats.Fetched)
		require.Equal(t, "partial", partial.Status)
		rawCount(f.ctx, t, f.database, "schema_baseline", 0)
		f.rebuild(t)
		f.execute(t, partial)
		rawCount(f.ctx, t, f.database, "schema_baseline", 0)
		f.execute(t, f.readRun(t, run.ID))
		require.Equal(t, "success", f.readRun(t, run.ID).Status)
		if cycle < 2 {
			rawCount(f.ctx, t, f.database, "schema_baseline", 0)
		}
	}
	rawCount(f.ctx, t, f.database, "schema_baseline", 1)
	rawCount(f.ctx, t, f.database, "schema_drift_event", 0)
	for _, table := range []string{"raw_payload", "raw_dedup", "listing", "listing_sku"} {
		rawCount(f.ctx, t, f.database, table, 500)
	}
	f.platform.Fault = "drift"
	f.rebuild(t)
	run := f.run(t)
	f.server.SetTime(f.now.Add(1000 * time.Second))
	f.execute(t, run)
	require.True(t, f.readRun(t, run.ID).Stats.Pending)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var drift repo.DriftEvent
	require.NoError(t, q.Table("schema_drift_event").Take(&drift).Error)
	require.Equal(t, "field_added", drift.Kind)
	require.Equal(t, "$.drift_field", drift.Path)
	f.execute(t, f.readRun(t, run.ID))
	f.execute(t, f.readRun(t, run.ID))
	rawCount(f.ctx, t, f.database, "schema_drift_event", 1)
	var active schemaBaseline
	require.NoError(t, q.Table("schema_baseline").Where("status='active'").Take(&active).Error)
	require.NotContains(t, active.Fields, "$.drift_field")
	metrics, err := (&syncMetricsStore{database: f.database}).Snapshot(f.ctx)
	require.NoError(t, err)
	require.Equal(t, float64(1), metrics.DriftOpen)
}

func TestSyncDriftIntegrationFailedEmptyAndQuarantinedCyclesExcluded(t *testing.T) {
	f := newSyncFixture(t, []json.RawMessage{})
	require.Equal(t, "success", completeCycle(t, f).Status)
	f.platform = mock.NewDataset(syncDataset(t, 1))
	f.rebuild(t)
	f.platform.Fault = "500"
	run := f.run(t)
	for i := 0; i < 3; i++ {
		f.execute(t, f.readRun(t, run.ID))
	}
	require.Equal(t, "failed", f.readRun(t, run.ID).Status)
	items := syncDataset(t, 1)
	items[0] = json.RawMessage(`{"id":"product-001","price":1,"currency":"CNY","status":"online","stock":1}`)
	f.platform = mock.NewDataset(items)
	f.rebuild(t)
	require.Equal(t, "partial", completeCycle(t, f).Status)
	f.platform = mock.NewDataset(syncDataset(t, 1))
	f.rebuild(t)
	for i := 0; i < 2; i++ {
		require.Equal(t, "success", completeCycle(t, f).Status)
	}
	rawCount(f.ctx, t, f.database, "schema_baseline", 0)
	require.Equal(t, "success", completeCycle(t, f).Status)
	rawCount(f.ctx, t, f.database, "schema_baseline", 1)
}

func TestSyncDriftIntegrationRawQuarantineAndNotificationAtomicity(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 2))
	warmSchema(t, f)
	items := syncDataset(t, 2)
	items[1] = json.RawMessage(`{"id":"product-002","price":1234,"currency":"CNY","status":"online","stock":10,"skus":[{"id":"sku-002"},{}],"raw_extra":true}`)
	f.platform = mock.NewDataset(items)
	f.rebuild(t)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION fail_inbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected inbox failure'; END $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER fail_inbox BEFORE INSERT ON notification FOR EACH ROW EXECUTE FUNCTION fail_inbox()`))
	run := f.run(t)
	f.server.SetTime(f.now.Add(1000 * time.Second))
	require.Error(t, f.engine.Execute(f.ctx, f.jobID, run.ID))
	rawCount(f.ctx, t, f.database, "schema_drift_event", 0)
	rawCount(f.ctx, t, f.database, "notification", 0)
	rawCount(f.ctx, t, f.database, "quarantine_record", 0)
	require.Zero(t, f.readRun(t, run.ID).Stats.Fetched)
	rawCount(f.ctx, t, f.database, "raw_payload", 3)
	require.NoError(t, f.database.Exec(f.ctx, `DROP TRIGGER fail_inbox ON notification`))
	f.rebuild(t)
	f.execute(t, run)
	result := f.readRun(t, run.ID)
	require.Equal(t, "partial", result.Status)
	require.Equal(t, 1, result.Stats.Quarantined)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []repo.DriftEvent
	require.NoError(t, q.Table("schema_drift_event").Order("path").Find(&events).Error)
	paths := []string{}
	critical := 0
	for _, event := range events {
		paths = append(paths, event.Path)
		if event.Severity == "critical" {
			critical++
		}
	}
	require.Contains(t, paths, "$.raw_extra")
	require.Equal(t, 2, critical) // Root title and one missing SKU ID, despite sibling values.
	rawCount(f.ctx, t, f.database, "notification", 2)
	f.noLeaks(t, "logs", f.logs.text())
}

func TestSyncDriftIntegrationPageTypeUnionDoesNotMisclassifyRecords(t *testing.T) {
	items := syncDataset(t, 2)
	var item map[string]any
	require.NoError(t, json.Unmarshal(items[1], &item))
	item["stock"] = "10"
	changed, err := json.Marshal(item)
	require.NoError(t, err)
	items[1] = changed
	f := newSyncFixture(t, items)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stored struct{ Spec json.RawMessage }
	require.NoError(t, q.Table("mapping_version").Select("spec").Where("id=?", f.mappingID).Take(&stored).Error)
	var spec ingestion.Spec
	require.NoError(t, json.Unmarshal(stored.Spec, &spec))
	for i := range spec.Fields {
		if spec.Fields[i].Target == "stock_total" {
			spec.Fields[i].Transform = "int(value)"
		}
	}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	compiled, err := ingestion.Compile(raw)
	require.NoError(t, err)
	require.NoError(t, q.Table("mapping_version").Where("id=?", f.mappingID).Updates(map[string]any{"spec": json.RawMessage(raw), "checksum": compiled.Checksum}).Error)
	f.rebuild(t)
	baseline := warmSchema(t, f)
	require.Equal(t, "number|string", baseline.Fields["$.stock"])
	require.Equal(t, "success", completeCycle(t, f).Status)
	rawCount(f.ctx, t, f.database, "schema_drift_event", 0)
	rawCount(f.ctx, t, f.database, "notification", 0)
}
