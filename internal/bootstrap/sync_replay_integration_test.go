package bootstrap

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

// REQ-SYNC-007: single-record retry uses its frozen mapping and no normal cursor.
func TestSyncIntegrationDeadLetterReplay(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 1))
	run := f.run(t)
	f.execute(t, run)
	before := f.cursor(t)
	id := seedSyncReplay(t, f, run, "dead_letter")
	replaceSyncJobMapping(t, f)
	response, envelope := shopHTTPRequest(t, f.router, http.MethodPost, "/api/v1/admin/dead-letters/"+itoa(id)+"/replay", nil, f.bearer)
	require.Equal(t, 201, response.Code, response.Body.String())
	var replay repo.Run
	require.NoError(t, json.Unmarshal(envelope.Data, &replay))
	require.Equal(t, "retry", replay.Trigger)
	require.Equal(t, f.mappingID, replay.Stats.MappingVersion)
	f.rebuild(t)
	f.execute(t, replay)
	result := f.readRun(t, replay.ID)
	require.Equal(t, "success", result.Status)
	require.Equal(t, 1, result.Stats.Created)
	require.Equal(t, before, f.cursor(t))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var status string
	require.NoError(t, q.Table("dead_letter").Select("status").Where("id=?", id).Take(&status).Error)
	require.Equal(t, "replayed", status)
	rawCount(f.ctx, t, f.database, "listing", 1)
	rawCount(f.ctx, t, f.database, "raw_payload", 1)
}

func TestSyncIntegrationQuarantineReplay(t *testing.T) {
	items := syncDataset(t, 1)
	items[0] = json.RawMessage(`{"id":"product-001","title":42,"price":1234,"currency":"CNY","status":"online","stock":10}`)
	f := newSyncFixture(t, items)
	run := f.run(t)
	f.execute(t, run)
	before := f.cursor(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var id int64
	require.NoError(t, q.Table("quarantine_record").Select("id").Take(&id).Error)
	replaceSyncJobMapping(t, f)
	response, envelope := shopHTTPRequest(t, f.router, http.MethodPost, "/api/v1/admin/quarantines/"+itoa(id)+"/replay", nil, f.bearer)
	require.Equal(t, 201, response.Code, response.Body.String())
	var replay repo.Run
	require.NoError(t, json.Unmarshal(envelope.Data, &replay))
	require.Equal(t, f.mappingID, replay.Stats.MappingVersion)
	f.execute(t, replay)
	result := f.readRun(t, replay.ID)
	require.Equal(t, "partial", result.Status)
	require.Equal(t, 1, result.Stats.Quarantined)
	require.Equal(t, before, f.cursor(t))
	require.Len(t, result.Errors, 1)
	require.Equal(t, "type_mismatch", result.Errors[0].ErrorCode)
	rawCount(f.ctx, t, f.database, "quarantine_record", 1)
	rawCount(f.ctx, t, f.database, "listing", 0)
}

func seedSyncReplay(t *testing.T, f *syncFixture, run repo.Run, kind string) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var raw repo.ReplayRaw
	require.NoError(t, q.Table("raw_payload").Select("id AS raw_id,fetched_at").Take(&raw).Error)
	raw.JobID, raw.RunID, raw.MappingVersion = f.jobID, run.ID, f.mappingID
	metadata, err := json.Marshal(raw)
	require.NoError(t, err)
	row := map[string]any{"tenant_id": 1, "shop_id": f.shop.ID, "resource_type": "product", "external_id": "product-001", "reason": "mapping_error", "payload": json.RawMessage(metadata)}
	require.NoError(t, q.Table(kind).Clauses(clause.Returning{}).Create(row).Error)
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM listing_sku WHERE listing_id IN (SELECT id FROM listing WHERE shop_id=?)", f.shop.ID))
	require.NoError(t, q.Table("listing").Where("shop_id=?", f.shop.ID).Delete(map[string]any{}).Error)
	return row["id"].(int64)
}

func replaceSyncJobMapping(t *testing.T, f *syncFixture) {
	t.Helper()
	// The current job can move on; replay must retain the source's mapping.
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO mapping_version (tenant_id,profile_id,version_no,status,spec,checksum)
SELECT tenant_id,profile_id,version_no+1,'published',spec,checksum FROM mapping_version WHERE id=?`, f.mappingID))
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sync_job SET options=jsonb_set(options,'{mapping_version_id}',to_jsonb((SELECT max(id) FROM mapping_version))) WHERE id=?`, f.jobID))
}
