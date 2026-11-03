package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/connector/platforms/mock"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestSyncIntegrationPublishedStatusSnapshot500Records(t *testing.T) {
	f := newSyncFixture(t, syncDataset(t, 500))
	require.Equal(t, "success", completeCycle(t, f).Status)
	profile, request := seedSnapshotDraft(t, f)
	program, result, err := PublishMapping(f.ctx, f.database, profile, 0, request)
	require.NoError(t, err)
	require.Equal(t, 20, result.Checked)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sync_job SET options=jsonb_set(options,'{mapping_version_id}',to_jsonb(?::bigint)) WHERE id=?`, request.VersionID, f.jobID))
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE sys_setting SET value='{"online":"offline"}' WHERE group_key='status_map'`))
	f.rebuild(t) // Fresh worker/cache compiles the published spec from PostgreSQL.
	run := completeCycle(t, f)
	require.Equal(t, "success", run.Status)
	require.Equal(t, 500, run.Stats.Fetched)
	require.Equal(t, 500, run.Stats.Updated)
	for _, table := range []string{"raw_payload", "raw_dedup", "listing", "listing_sku"} {
		rawCount(f.ctx, t, f.database, table, 500)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("listing").Where("status='online'").Count(&count).Error)
	require.EqualValues(t, 500, count)
	var version struct {
		Spec     json.RawMessage
		Checksum string
	}
	require.NoError(t, q.Table("mapping_version").Where("id=?", request.VersionID).Take(&version).Error)
	cold, err := ingestion.CompileFromVersion(version.Spec)
	require.NoError(t, err)
	require.Equal(t, program.Checksum, cold.Checksum)
	require.Equal(t, version.Checksum, cold.Checksum)
	// Unknown status must be isolated, never coerced to online/offline.
	items := syncDataset(t, 500)
	items[0] = json.RawMessage(strings.Replace(string(items[0]), `"online"`, `"unrecognized"`, 1))
	f.platform = mock.NewDataset(items)
	f.rebuild(t)
	run = completeCycle(t, f)
	require.Equal(t, "partial", run.Status)
	require.Equal(t, 1, run.Stats.Quarantined)
	var reason string
	require.NoError(t, q.Table("quarantine_record").Select("reason_code").Take(&reason).Error)
	require.Equal(t, "invalid_value", reason)
	require.NoError(t, q.Table("listing").Where("status='online'").Count(&count).Error)
	require.EqualValues(t, 500, count)
}
