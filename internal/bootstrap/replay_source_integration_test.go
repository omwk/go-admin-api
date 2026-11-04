package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	syncmodule "github.com/omwk/go-admin-api/internal/modules/sync"
	"github.com/stretchr/testify/require"
)

func readReplayTargets(t *testing.T, f *syncFixture) (catalog.Listing, []catalog.ListingSKU) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var parent catalog.Listing
	var children []catalog.ListingSKU
	require.NoError(t, q.Take(&parent).Error)
	require.NoError(t, q.Order("id").Find(&children).Error)
	return parent, children
}

func processReplay(t *testing.T, f *syncFixture, r syncmodule.ReplayRun) syncmodule.ReplayRun {
	t.Helper()
	result, err := f.engineStore.ProcessReplay(f.ctx, 1, r.ID, r.Processed)
	require.NoError(t, err)
	require.Equal(t, result.Processed, result.Stats.Processed())
	return result
}

func TestReplayIntegrationStaleGroupAndSameRawNewChecksum(t *testing.T) {
	// REQ-SYNC-011: old source never mutates parent, children, stock or summaries.
	f := replayFixture(t, 1)
	first := processReplay(t, f, createReplay(t, f))
	require.EqualValues(t, 1, first.Stats.Inserted)
	parent, skus := readReplayTargets(t, f)
	same := processReplay(t, f, createReplay(t, f))
	require.EqualValues(t, 1, same.Stats.SkippedSame)
	unchanged, children := readReplayTargets(t, f)
	require.Equal(t, parent, unchanged)
	require.Equal(t, skus, children)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var original struct {
		Spec      json.RawMessage
		ProfileID int64
	}
	require.NoError(t, q.Table("mapping_version").Select("spec,profile_id").Where("id=?", f.mappingID).Take(&original).Error)
	var spec ingestion.Spec
	require.NoError(t, json.Unmarshal(original.Spec, &spec))
	for i := range spec.Fields {
		if spec.Fields[i].Target == "title" {
			spec.Fields[i].Transform = "upper(value)"
		}
	}
	encoded, err := json.Marshal(spec)
	require.NoError(t, err)
	compiled, err := ingestion.CompileFromVersion(encoded)
	require.NoError(t, err)
	var version struct{ ID int64 }
	require.NoError(t, q.Raw(`INSERT INTO mapping_version(tenant_id,profile_id,version_no,status,spec,checksum) VALUES(1,?,2,'published',?,?) RETURNING id`, original.ProfileID, encoded, compiled.Checksum).Scan(&version).Error)
	request := replayRequest(f)
	request.MappingVersionID = version.ID
	reinterpret, err := f.engineStore.CreateReplay(f.ctx, request, nil)
	require.NoError(t, err)
	// Active job mapping moves independently of the frozen run.
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sync_job SET options=jsonb_set(options,'{mapping_version_id}',to_jsonb(?::bigint)) WHERE id=?`, version.ID, f.jobID))
	reinterpret = processReplay(t, f, reinterpret)
	require.EqualValues(t, 1, reinterpret.Stats.Updated)
	changed, children := readReplayTargets(t, f)
	require.Equal(t, "MOCK PRODUCT", changed.Title)
	require.Equal(t, *parent.RawID, *changed.RawID)
	require.Len(t, children, 1)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 2)
	// Freeze only the old raw, then accept a newer normal sync before replay.
	request.Scope = syncmodule.ReplayScope{Type: "ids", IDs: []int64{*parent.RawID, *parent.RawID}}
	stale, err := f.engineStore.CreateReplay(f.ctx, request, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, stale.Total)
	newer := ingestion.RawItem{ExternalID: parent.ExternalProductID, Payload: []byte(`{"id":"product-001","title":"latest","price":9999,"currency":"CNY","status":"online","stock":99,"skus":[{"id":"sku-001","price":9999,"stock":99}]}`)}
	refs, err := rawServiceFor(t, f.database, 1, f.shop.PlatformID).SaveRaw(f.ctx, f.shop.ID, "product", []ingestion.RawItem{newer})
	require.NoError(t, err)
	latestRequest := replayRequest(f)
	latestRequest.Scope = syncmodule.ReplayScope{Type: "ids", IDs: []int64{refs[0].ID}}
	latest, err := f.engineStore.CreateReplay(f.ctx, latestRequest, nil)
	require.NoError(t, err)
	processReplay(t, f, latest)
	before, beforeChildren := readReplayTargets(t, f)
	stale = processReplay(t, f, stale)
	require.EqualValues(t, 1, stale.Stats.SkippedStale)
	after, afterChildren := readReplayTargets(t, f)
	require.Equal(t, before, after)
	require.Equal(t, beforeChildren, afterChildren)
	rawCount(f.ctx, t, f.database, "inventory_snapshot", 3)
}

func TestReplayIntegrationUncertainSourceQuarantinesWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name, ext, reason string
		ambiguous         bool
	}{
		{"missing", "{}", "source_metadata_unknown", false},
		{"invalid time", `{"_ingestion":{"source_order":1,"source_fetched_at":"bad","mapping_checksum":"old"}}`, "source_metadata_unknown", false},
		{"contradictory order", `{"_ingestion":{"source_order":999,"source_fetched_at":"2026-11-01T00:00:00Z","mapping_checksum":"old"}}`, "source_identity_ambiguous", false},
		{"ambiguous directory", "", "source_identity_ambiguous", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := replayFixture(t, 1)
			processReplay(t, f, createReplay(t, f))
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			if tc.ambiguous {
				require.NoError(t, q.Exec(`INSERT INTO raw_dedup(tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,raw_id,fetched_at) SELECT tenant_id,platform_id,shop_id,resource_type,external_id,'ambiguous-copy',raw_id,fetched_at FROM raw_dedup LIMIT 1`).Error)
			} else {
				require.NoError(t, q.Table("listing").Where("shop_id=?", f.shop.ID).Update("ext", json.RawMessage(tc.ext)).Error)
			}
			before, beforeChildren := readReplayTargets(t, f)
			run := processReplay(t, f, createReplay(t, f))
			require.EqualValues(t, 1, run.Stats.Quarantined)
			require.Equal(t, "partial", run.Status)
			after, afterChildren := readReplayTargets(t, f)
			require.Equal(t, before, after)
			require.Equal(t, beforeChildren, afterChildren)
			rawCount(f.ctx, t, f.database, "inventory_snapshot", 1)
			var reason string
			require.NoError(t, q.Table("quarantine_record").Select("reason_code").Take(&reason).Error)
			require.Equal(t, tc.reason, reason)
		})
	}
}

func TestReplayIntegrationRevokedGenerationAndTenantIsolation(t *testing.T) {
	f := replayFixture(t, 1)
	run := createReplay(t, f)
	_, err := f.engineStore.ProcessReplay(f.ctx, 2, run.ID, 0)
	require.Error(t, err)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sync_job SET options=jsonb_set(options,'{authorization_generation}','2') WHERE id=?`, f.jobID))
	_, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.ErrorIs(t, err, errRunAuthorizationRevoked)
	rawCount(f.ctx, t, f.database, "listing", 0)
	persisted, err := f.engineStore.ReadReplay(f.ctx, run.ID)
	require.NoError(t, err)
	require.Zero(t, persisted.Processed)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sync_job SET options=jsonb_set(options,'{authorization_generation}','1') WHERE id=?`, f.jobID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET status='disabled' WHERE id=?", run.TriggeredBy))
	_, err = f.engineStore.ProcessReplay(f.ctx, 1, run.ID, 0)
	require.Error(t, err)
	rawCount(f.ctx, t, f.database, "listing", 0)
}

func TestReplayIntegrationFreezeTimeUsesHalfOpenBounds(t *testing.T) {
	f := replayFixture(t, 1)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var fetched time.Time
	require.NoError(t, q.Table("raw_payload").Select("fetched_at").Take(&fetched).Error)
	request := replayRequest(f)
	request.Scope.From = &fetched
	to := fetched.Add(time.Microsecond)
	request.Scope.To = &to
	run, err := f.engineStore.CreateReplay(f.ctx, request, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, run.Total)
	request.Scope.From = &to
	end := to.Add(time.Microsecond)
	request.Scope.To = &end
	run, err = f.engineStore.CreateReplay(f.ctx, request, nil)
	require.NoError(t, err)
	require.Zero(t, run.Total)
}
