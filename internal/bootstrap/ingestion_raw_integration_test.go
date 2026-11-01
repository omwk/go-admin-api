package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func rawServiceFor(t *testing.T, database *db.DB, tenant, platform int64) *ingestion.Service {
	t.Helper()
	service, err := NewRawService(database, func(context.Context, int64) (ingestion.RawIdentity, error) {
		return ingestion.RawIdentity{TenantID: tenant, PlatformID: platform}, nil
	})
	require.NoError(t, err)
	return service
}

func rawMonth(t *testing.T, value string) ingestion.Month {
	t.Helper()
	month, err := ingestion.ParseMonth(value)
	require.NoError(t, err)
	return month
}

func rawTime(t *testing.T, value string) time.Time {
	t.Helper()
	stamp, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return stamp
}

func rawCount(ctx context.Context, t *testing.T, database *db.DB, table string, expected int64) {
	t.Helper()
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table(table).Count(&count).Error)
	require.Equal(t, expected, count, table)
}

func TestRawIntegrationCrossMonthSemanticDedup(t *testing.T) {
	// REQ-SYNC-003: UTC half-open routing and cross-fetch semantic dedup.
	database, ctx := isolatedIdentityDB(t)
	partitions := NewRawPartitionStore(database)
	for _, value := range []string{"2040-01", "2040-02"} {
		_, err := partitions.Ensure(ctx, rawMonth(t, value))
		require.NoError(t, err)
	}
	payload, err := os.ReadFile("../modules/ingestion/testdata/raw_original.json")
	require.NoError(t, err)
	items := []ingestion.RawItem{
		{ExternalID: "a", Payload: payload, FetchedAt: rawTime(t, "2040-01-31T23:59:59Z")},
		{ExternalID: "b", Payload: payload, FetchedAt: rawTime(t, "2040-02-01T08:00:00+08:00")},
	}
	service := rawServiceFor(t, database, 1, 1)
	refs, err := service.SaveRaw(ctx, 1, "product", items)
	require.NoError(t, err)
	require.Len(t, refs, 2)
	require.True(t, refs[0].IsNew)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var routes []struct{ ExternalID, Partition string }
	require.NoError(t, q.Raw("SELECT external_id, tableoid::regclass::text AS partition FROM raw_payload ORDER BY external_id").Scan(&routes).Error)
	require.Equal(t, "raw_payload_204001", routes[0].Partition)
	require.Equal(t, "raw_payload_204002", routes[1].Partition)
	for _, name := range []string{"raw_whitespace.json", "raw_reordered.json"} {
		payload, err := os.ReadFile("../modules/ingestion/testdata/" + name)
		require.NoError(t, err)
		for i := range items {
			items[i].Payload = payload
			items[i].FetchedAt = items[i].FetchedAt.AddDate(0, 1, 0)
		}
		repeated, err := service.SaveRaw(ctx, 1, "product", items)
		require.NoError(t, err) // Existing references need no new partition.
		for i, ref := range repeated {
			require.Equal(t, refs[i].ID, ref.ID)
			require.True(t, refs[i].FetchedAt.Equal(ref.FetchedAt))
			require.False(t, ref.IsNew)
		}
	}
	rawCount(ctx, t, database, "raw_payload", 2)
	rawCount(ctx, t, database, "raw_dedup", 2)
	var stored string
	require.NoError(t, q.Raw("SELECT payload::text FROM raw_payload WHERE external_id = 'a'").Scan(&stored).Error)
	require.JSONEq(t, string(payload), stored)
	items[0].Payload = json.RawMessage(`{"id":"fixture-1","amount":9007199254740994}`)
	items[0].FetchedAt = rawTime(t, "2040-02-02T00:00:00Z")
	changed, err := service.SaveRaw(ctx, 1, "product", items[:1])
	require.NoError(t, err)
	require.True(t, changed[0].IsNew)
	require.NotEqual(t, refs[0].ID, changed[0].ID)
	rawCount(ctx, t, database, "raw_payload", 3)
}

func TestRawIntegrationMissingPartitionRollsBackAndPreservesCursor(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	_, err := NewRawPartitionStore(database).Ensure(ctx, rawMonth(t, "2040-01"))
	require.NoError(t, err)
	require.NoError(t, database.Exec(ctx, `INSERT INTO sync_cursor (shop_id, resource_type, cursor_type, cursor_value)
        VALUES (1, 'product', 'page_token', 'page-before')`))
	service := rawServiceFor(t, database, 1, 1)
	items := []ingestion.RawItem{
		{ExternalID: "a", Payload: json.RawMessage(`{"id":"a"}`), FetchedAt: rawTime(t, "2040-01-31T23:59:59Z")},
		{ExternalID: "b", Payload: json.RawMessage(`{"id":"b"}`), FetchedAt: rawTime(t, "2040-02-01T00:00:00Z")},
	}
	refs, err := service.SaveRaw(ctx, 1, "product", items)
	require.ErrorIs(t, err, ingestion.ErrPartitionMissing)
	require.Nil(t, refs)
	rawCount(ctx, t, database, "raw_payload", 0)
	rawCount(ctx, t, database, "raw_dedup", 0)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var cursor string
	require.NoError(t, q.Raw("SELECT cursor_value FROM sync_cursor WHERE shop_id = 1 AND resource_type = 'product'").Scan(&cursor).Error)
	require.Equal(t, "page-before", cursor)
	_, err = NewRawPartitionStore(database).Ensure(ctx, rawMonth(t, "2040-02"))
	require.NoError(t, err)
	refs, err = service.SaveRaw(ctx, 1, "product", items)
	require.NoError(t, err)
	require.Len(t, refs, 2)
	rawCount(ctx, t, database, "raw_payload", 2)
}

func TestRawIntegrationConcurrentClaimsAndSixColumnIsolation(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	_, err := NewRawPartitionStore(database).Ensure(ctx, rawMonth(t, "2040-01"))
	require.NoError(t, err)
	items := []ingestion.RawItem{{ExternalID: "a", Payload: json.RawMessage(`{"n":1}`), FetchedAt: rawTime(t, "2040-01-02T00:00:00Z")}}
	service := rawServiceFor(t, database, 1, 1)
	type result struct {
		refs []ingestion.RawRef
		err  error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			refs, err := service.SaveRaw(ctx, 1, "product", items)
			results <- result{refs, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	created, id := 0, int64(0)
	for result := range results {
		require.NoError(t, result.err)
		ref := result.refs[0]
		if id == 0 {
			id = ref.ID
		}
		require.Equal(t, id, ref.ID)
		if ref.IsNew {
			created++
		}
	}
	require.Equal(t, 1, created)
	for _, tc := range []struct {
		tenant, platform, shop int64
		resource               string
	}{
		{2, 1, 1, "product"}, {1, 2, 1, "product"}, {1, 1, 2, "product"}, {1, 1, 1, "order"},
	} {
		refs, err := rawServiceFor(t, database, tc.tenant, tc.platform).SaveRaw(ctx, tc.shop, tc.resource, items)
		require.NoError(t, err)
		require.True(t, refs[0].IsNew)
	}
	items[0].ExternalID = "b"
	_, err = service.SaveRaw(ctx, 1, "product", items)
	require.NoError(t, err)
	items[0].Payload = json.RawMessage(`{"n":2}`)
	_, err = service.SaveRaw(ctx, 1, "product", items)
	require.NoError(t, err)
	rawCount(ctx, t, database, "raw_payload", 7)
	rawCount(ctx, t, database, "raw_dedup", 7)
}

func TestRawIntegrationBatchLimitAndRepeatedItems(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	_, err := NewRawPartitionStore(database).Ensure(ctx, rawMonth(t, "2040-01"))
	require.NoError(t, err)
	items := make([]ingestion.RawItem, 1001)
	for i := range items {
		items[i] = ingestion.RawItem{ExternalID: fmt.Sprint(i), Payload: json.RawMessage(`{"n":1}`), FetchedAt: rawTime(t, "2040-01-01T00:00:00Z")}
	}
	service := rawServiceFor(t, database, 1, 1)
	refs, err := service.SaveRaw(ctx, 1, "product", append(items, items[0]))
	require.NoError(t, err)
	require.Len(t, refs, 1002)
	require.Equal(t, refs[0].ID, refs[1001].ID)
	require.False(t, refs[1001].IsNew)
	for i := range items {
		require.Equal(t, items[i].ExternalID, refs[i].ExternalID)
	}
	rawCount(ctx, t, database, "raw_payload", 1001)
}
