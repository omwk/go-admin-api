package bootstrap

import (
	"context"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestRawPartitionIntegrationConcurrentAndValidated(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	store := NewRawPartitionStore(database)
	month := rawMonth(t, "2040-02")
	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			created, err := store.Ensure(ctx, month)
			results <- result{created, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	created := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.created {
			created++
		}
	}
	require.Equal(t, 1, created)
	covered, err := store.Covered(ctx, month)
	require.NoError(t, err)
	require.True(t, covered)
	for _, invalid := range []string{"", "2040-2", "2040-02-01", "2040-13", "2040-00", "0000-01", "9999-12", "2040-02'); DROP TABLE raw_payload;--"} {
		require.Error(t, database.Exec(ctx, "SELECT ensure_raw_partition(?)", invalid))
	}
	_, err = store.Ensure(ctx, ingestion.Month{})
	require.Error(t, err)
	require.NoError(t, database.Transaction(ctx, func(tx context.Context) error {
		require.NoError(t, database.Exec(tx, "SET LOCAL TIME ZONE 'Asia/Shanghai'"))
		covered, err := store.Covered(tx, month)
		require.NoError(t, err)
		require.True(t, covered)
		return nil
	}))
}

func TestRawPartitionIntegrationRestartAfterOfflineMonths(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	store := NewRawPartitionStore(database)
	var alerts []string
	maintainer, err := ingestion.NewPartitionMaintainer(store, func(_ context.Context, month ingestion.Month, _ error) { alerts = append(alerts, month.String()) })
	require.NoError(t, err)
	require.NoError(t, maintainer.Maintain(ctx, rawTime(t, "2040-01-31T23:59:59Z")))
	require.Equal(t, []string{"2040-01", "2040-02", "2040-03"}, alerts)
	alerts = nil
	require.NoError(t, maintainer.Maintain(ctx, rawTime(t, "2040-05-01T08:00:00+08:00")))
	require.Equal(t, []string{"2040-05", "2040-06", "2040-07"}, alerts)
	for _, value := range alerts {
		covered, err := store.Covered(ctx, rawMonth(t, value))
		require.NoError(t, err)
		require.True(t, covered)
	}
	alerts = nil
	require.NoError(t, maintainer.Maintain(ctx, rawTime(t, "2040-05-02T00:00:00Z")))
	require.Empty(t, alerts)
}

func TestRawPartitionIntegrationIncorrectExistingBoundsAlert(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	require.NoError(t, database.Exec(ctx, `CREATE TABLE raw_payload_204004 PARTITION OF raw_payload
		FOR VALUES FROM ('2040-04-02T00:00:00Z') TO ('2040-05-01T00:00:00Z')`))
	store := NewRawPartitionStore(database)
	month := rawMonth(t, "2040-04")
	covered, err := store.Covered(ctx, month)
	require.NoError(t, err)
	require.False(t, covered)
	_, err = store.Ensure(ctx, month)
	require.ErrorContains(t, err, "raw partition bounds mismatch")
	var alerts []string
	maintainer, err := ingestion.NewPartitionMaintainer(store, func(_ context.Context, month ingestion.Month, _ error) {
		alerts = append(alerts, month.String())
	})
	require.NoError(t, err)
	require.ErrorContains(t, maintainer.Maintain(ctx, rawTime(t, "2040-04-01T00:00:00Z")), "raw partition bounds mismatch")
	require.Equal(t, []string{"2040-04", "2040-04", "2040-05", "2040-06"}, alerts)
	covered, err = store.Covered(ctx, rawMonth(t, "2040-06"))
	require.NoError(t, err)
	require.True(t, covered)
}
