package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestCatalogProductIntegrationMergeSummaryAndSyncPriceRefresh(t *testing.T) {
	f := newSyncFixture(t, nil)
	ctx := f.ctx
	signer, err := catalog.NewProductCursorSigner([]byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	service, err := catalog.NewProductService(NewProductStore(f.database), func(ctx context.Context, _, _ int64) (context.Context, error) {
		return ctx, nil
	}, signer)
	require.NoError(t, err)
	created, err := service.Create(ctx, 1, f.actor.UserID, catalog.ProductCreateRequest{Title: "Manual master", Status: "active"})
	require.NoError(t, err)
	require.Equal(t, "manual", created.Product.Source)
	listingIDs := make([]int64, 0, 3)
	q, cancel := f.database.WithContext(ctx)
	for i, price := range []int64{500, 1000, 1500} {
		listing := catalog.Listing{TenantID: 1, ShopID: f.shop.ID, PlatformID: f.shop.PlatformID, ExternalProductID: fmt.Sprintf("catalog-listing-%d", i+1),
			Title: "Platform title", PriceCents: price, Currency: "CNY", Status: "online", SyncedAt: time.Now().UTC(), Ext: json.RawMessage(`{"_ingestion":{"raw_id":99}}`)}
		require.NoError(t, q.Create(&listing).Error)
		listingIDs = append(listingIDs, listing.ID)
		sku := catalog.ListingSKU{TenantID: 1, ListingID: listing.ID, ExternalSKUID: fmt.Sprintf("catalog-sku-%d", i+1), Title: "Platform variant", PriceCents: price, Stock: 1,
			SyncedAt: time.Now().UTC(), Ext: json.RawMessage(`{"_ingestion":{"raw_id":100}}`)}
		require.NoError(t, q.Create(&sku).Error)
	}
	cancel()
	merged, err := service.Merge(ctx, 1, f.actor.UserID, created.Product.ID, listingIDs)
	require.NoError(t, err)
	require.Equal(t, 3, merged.Product.SKUCount)
	require.EqualValues(t, 3, merged.ReportedStock)
	require.EqualValues(t, 500, merged.Product.PriceMinCents)
	require.EqualValues(t, 1500, merged.Product.PriceMaxCents)
	require.Equal(t, "Manual master", merged.Product.Title)
	page, err := service.List(ctx, 1, f.actor.UserID, catalog.ProductListQuery{ProductFilter: catalog.ProductFilter{ShopID: f.shop.ID}, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List[0].ShopNames, 1)
	_, err = service.Get(ctx, 2, f.actor.UserID, created.Product.ID)
	require.Error(t, err, "another tenant must not see this product")

	mapped := ingestion.Mapped{TenantID: 1, ShopID: f.shop.ID, PlatformID: f.shop.PlatformID, ExternalID: "catalog-listing-1",
		Values:          map[string]any{"external_product_id": "catalog-listing-1", "title": "New platform title", "price_cents": int64(2000), "currency": "CNY", "status": "online"},
		Collections:     map[string][]map[string]any{"skus": {{"external_sku_id": "catalog-sku-1", "title": "Platform variant", "price_cents": int64(2000), "stock": int64(2)}}},
		SourceFetchedAt: time.Now().UTC().Add(time.Minute), SourceOrder: 10, MappingChecksum: "product-mapping-v2"}
	require.NoError(t, f.database.Transaction(ctx, func(tx context.Context) error {
		handle, cancel := f.database.WithContext(tx)
		defer cancel()
		_, upsertErr := (catalog.Upserter{}).Upsert(tx, handle, ingestion.ResourceProduct, []ingestion.Mapped{mapped})
		return upsertErr
	}))
	refreshed, err := service.Get(ctx, 1, f.actor.UserID, created.Product.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1000, refreshed.Product.PriceMinCents)
	require.EqualValues(t, 2000, refreshed.Product.PriceMaxCents)
	require.EqualValues(t, 4, refreshed.ReportedStock)
	require.Equal(t, "Manual master", refreshed.Product.Title, "platform sync must not overwrite manual SPU fields")
	response, err := json.Marshal(refreshed)
	require.NoError(t, err)
	require.NotContains(t, string(response), catalog.ReservedExtKey)
	require.NotContains(t, string(response), "raw_id")

	q, cancel = f.database.WithContext(ctx)
	var boundSKU catalog.ProductSKU
	require.NoError(t, q.Where("tenant_id=? AND spu_id=? AND deleted_at IS NULL", 1, created.Product.ID).Order("id").Take(&boundSKU).Error)
	code := "retained-platform-sku"
	require.NoError(t, q.Model(&catalog.ProductSKU{}).Where("id=?", boundSKU.ID).Update("code", code).Error)
	var listingSKU catalog.ListingSKU
	require.NoError(t, q.Where("tenant_id=? AND listing_id=? AND external_sku_id=?", 1, listingIDs[0], "catalog-sku-1").Take(&listingSKU).Error)
	cancel()
	_, err = service.Update(ctx, 1, f.actor.UserID, created.Product.ID, catalog.ProductUpdateRequest{Version: refreshed.Product.Version,
		Title: "Manual master", Status: "active", SKUs: &[]catalog.ProductSKUInput{{Code: &code, Barcode: "", Title: "Retained variant", PriceCents: 2000, CostCents: 0, Currency: "CNY"}}})
	require.NoError(t, err)
	updated, err := service.Get(ctx, 1, f.actor.UserID, created.Product.ID)
	require.NoError(t, err)
	require.Equal(t, 1, updated.Product.SKUCount)
	q, cancel = f.database.WithContext(ctx)
	var retainedID int64
	require.NoError(t, q.Table("listing_sku").Select("sku_id").Where("tenant_id=? AND id=?", 1, listingSKU.ID).Take(&retainedID).Error)
	cancel()
	require.Equal(t, boundSKU.ID, retainedID)
}

func TestCatalogProductIntegrationHundredThousandListLatencyAndDeepCursor(t *testing.T) {
	f := newSyncFixture(t, nil)
	rows := make([]catalog.ProductSPU, 100000)
	for i := range rows {
		rows[i] = catalog.ProductSPU{TenantID: 1, Title: fmt.Sprintf("Perf product %06d", i), Status: "active", Source: "manual", Attributes: []byte(`{}`), Ext: []byte(`{}`)}
	}
	for start := 0; start < len(rows); start += 500 {
		end := start + 500
		if end > len(rows) {
			end = len(rows)
		}
		q, cancel := f.database.WithContext(f.ctx)
		err := q.CreateInBatches(rows[start:end], 500).Error
		cancel()
		require.NoError(t, err)
	}
	signer, err := catalog.NewProductCursorSigner([]byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	service, err := catalog.NewProductService(NewProductStore(f.database), func(ctx context.Context, _, _ int64) (context.Context, error) { return ctx, nil }, signer)
	require.NoError(t, err)
	durations := make([]time.Duration, 25)
	for i := range durations {
		started := time.Now()
		page, err := service.List(f.ctx, 1, f.actor.UserID, catalog.ProductListQuery{Page: 1, PageSize: 20})
		require.NoError(t, err)
		require.Len(t, page.List, 20)
		durations[i] = time.Since(started)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("100000 products, 25 first-page requests: P95=%s", durations[23])
	require.Less(t, durations[23], 500*time.Millisecond, "first-page P95 across 25 requests: %s", durations[23])

	query := catalog.ProductListQuery{Page: 100, PageSize: 20, Sort: "-updated_at"}
	page100, err := service.List(f.ctx, 1, f.actor.UserID, query)
	require.NoError(t, err)
	require.NotEmpty(t, page100.NextCursor)
	query.Cursor = page100.NextCursor
	seen := map[int64]bool{}
	for i := 0; i < 5; i++ {
		page, err := service.List(f.ctx, 1, f.actor.UserID, query)
		require.NoError(t, err)
		require.Len(t, page.List, 20)
		for _, row := range page.List {
			require.False(t, seen[row.ID], "deep-page duplicate product %d", row.ID)
			seen[row.ID] = true
		}
		query.Cursor = page.NextCursor
	}
	require.Len(t, seen, 100)
}
