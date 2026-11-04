package bootstrap

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// capacitySize returns the member count used by the freeze budget test.
// CI runners are several times slower than the reference machine, so the
// default stays small enough to be a real assertion rather than a flaky
// timeout. Set REPLAY_CAPACITY_FULL=1 to run the 500000 acceptance size.
func capacitySize() int {
	if os.Getenv("REPLAY_CAPACITY_FULL") == "1" {
		return 500000
	}
	return 50000
}

func TestReplayIntegrationCapacityFreezeWithinBudget(t *testing.T) {
	// REQ-SYNC-011 / ADR-018: the maximum accepted online set uses the same 3s budget.
	size := capacitySize()
	f := replayFixture(t, 0)
	for from := 1; from <= size; from += 5000 {
		q, stop := f.database.WithContext(f.ctx)
		require.NoError(t, q.Exec(`INSERT INTO raw_dedup(tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,raw_id,fetched_at)
 SELECT 1,?,?,'product','capacity-'||n,n::text,n,date_trunc('second',now()) FROM generate_series(?::bigint,?::bigint) n`, f.shop.PlatformID, f.shop.ID, from, min(from+4999, size)).Error)
		require.NoError(t, q.Exec(`INSERT INTO raw_payload(id,tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,fetched_at,payload,payload_size) OVERRIDING SYSTEM VALUE
 SELECT raw_id,tenant_id,platform_id,shop_id,resource_type,external_id,payload_sha256,fetched_at,'{}',2 FROM raw_dedup WHERE raw_id BETWEEN ? AND ?`, from, min(from+4999, size)).Error)
		stop()
	}
	started := time.Now()
	run, err := f.engineStore.CreateReplay(f.ctx, replayRequest(f), nil)
	elapsed := time.Since(started)
	require.NoError(t, err, "%d member freeze took %s", size, elapsed)
	require.EqualValues(t, size, run.Total)
	require.Less(t, elapsed, 3*time.Second)
	t.Logf("%d member online freeze committed: %s (full size via REPLAY_CAPACITY_FULL=1)", size, elapsed)
}
