package bootstrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReplayIntegrationCapacityFreezeWithinBudget(t *testing.T) {
	// REQ-SYNC-011 / ADR-018: the maximum accepted online set uses the same 3s budget.
	f := replayFixture(t, 0)
	for from := 1; from <= 500000; from += 5000 {
		q, stop := f.database.WithContext(f.ctx)
		require.NoError(t, q.Exec(`INSERT INTO raw_dedup(tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,raw_id,fetched_at)
 SELECT 1,?,?,'product','capacity-'||n,n::text,n,date_trunc('second',now()) FROM generate_series(?::bigint,?::bigint) n`, f.shop.PlatformID, f.shop.ID, from, min(from+4999, 500000)).Error)
		require.NoError(t, q.Exec(`INSERT INTO raw_payload(id,tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,fetched_at,payload,payload_size) OVERRIDING SYSTEM VALUE
 SELECT raw_id,tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,fetched_at,'{}',2 FROM raw_dedup WHERE raw_id BETWEEN ? AND ?`, from, min(from+4999, 500000)).Error)
		stop()
	}
	started := time.Now()
	run, err := f.engineStore.CreateReplay(f.ctx, replayRequest(f), nil)
	elapsed := time.Since(started)
	require.NoError(t, err, "500000 member freeze took %s", elapsed)
	require.EqualValues(t, 500000, run.Total)
	require.Less(t, elapsed, 3*time.Second)
	t.Logf("500000 member online freeze committed: %s", elapsed)
}
