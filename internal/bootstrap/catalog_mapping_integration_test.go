package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/ingestion"
	"github.com/stretchr/testify/require"
)

func TestCatalogMappingIntegrationPoolManualFilterAndCandidates(t *testing.T) {
	// REQ-PRD-003: rule candidates never write assignments; manual maps drive search.
	f := newCategorySyncFixture(t, categoryTree(t))
	exactID := insertCategory(t, f, 0, "Category 01", "/Category 01", 1)
	insertCategory(t, f, 0, "Category 02", "/Category 02", 1)
	f.execute(t, f.run(t))
	store := NewCategoryManagementStore(f.database)
	pool, total, err := store.ListCategoryMappingPool(f.ctx, 1, 0, 1, 100, "false")
	require.NoError(t, err)
	require.EqualValues(t, 20, total)
	require.Len(t, pool, 20)
	first := categoryMapping(t, f, "category-01")
	for _, item := range pool {
		if item.Mapping.ID == first.ID {
			require.Nil(t, item.Mapping.CategoryID)
			require.Equal(t, []catalog.CategoryMappingCandidate{{CategoryID: exactID, Name: "Category 01", Path: "/Category 01", Confidence: 0.9}}, item.Candidates)
		}
	}
	op, err := idem.Prepare(1, f.actor.UserID, idem.Input{Key: "mapping-once", Method: "PUT", Route: "/category-mappings/:id", PathParams: []string{fmt.Sprint(first.ID)}, Body: []byte(fmt.Sprintf(`{"category_id":%d}`, exactID))})
	require.NoError(t, err)
	require.NoError(t, store.SetManualCategoryMapping(f.ctx, 1, first.ID, exactID, &op))
	manual := categoryMapping(t, f, "category-01")
	require.NoError(t, store.SetManualCategoryMapping(f.ctx, 1, first.ID, exactID, &op))
	require.Equal(t, manual, categoryMapping(t, f, "category-01"), "replay must preserve the first write timestamp")
	changed := op
	changed.RequestHash = "different-request"
	require.Equal(t, errs.ErrIdempotencyMismatch, errs.From(store.SetManualCategoryMapping(f.ctx, 1, first.ID, exactID, &changed)).Code)
	require.Equal(t, 40401, errs.From(store.SetManualCategoryMapping(f.ctx, 1, first.ID+9999, exactID)).Code)
	for matched, count := range map[string]int64{"false": 19, "true": 1, "all": 20} {
		_, total, err = store.ListCategoryMappingPool(f.ctx, 1, 0, 1, 100, matched)
		require.NoError(t, err)
		require.Equal(t, count, total)
	}
	_, total, err = store.ListCategoryMappingPool(f.ctx, 2, 0, 1, 100, "all")
	require.NoError(t, err)
	require.Zero(t, total)
	assertMappingCandidatesHTTP(t, f, manual)
	assertMappedProductFilter(t, f, exactID)
}

func assertMappingCandidatesHTTP(t *testing.T, f *syncFixture, manual catalog.CategoryMapping) {
	t.Helper()
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, catalog.RegisterCategoryRoutes(f.router, f.identityFixture.service, profiles, NewCategoryManagementStore(f.database)))
	require.NoError(t, catalog.RegisterInventoryRoutes(f.router, f.identityFixture.service, profiles, NewInventoryStore(f.database)))
	response, envelope := f.request(t, "POST", "/category-mappings:auto", map[string]any{"platform_id": f.shop.PlatformID})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Zero(t, envelope.Code)
	validateOpenAPIResponse(t, readCatalogOpenAPI(t), "/api/v1/admin/category-mappings:auto", "post", 200, response.Body.Bytes())
	require.NotContains(t, response.Body.String(), `"applied"`)
	require.Contains(t, response.Body.String(), `"confidence":0.9`)
	require.Nil(t, categoryMapping(t, f, "category-02").CategoryID)
	require.Equal(t, manual, categoryMapping(t, f, "category-01"))
	for path, status := range map[string]int{"/category-mappings?matched=all": 200, "/category-mappings?matched=wrong": 400, "/inventories?page_size=101": 400, "/inventories": 200} {
		response, _ := f.request(t, "GET", path, nil)
		require.Equal(t, status, response.Code, response.Body.String())
		if status == 200 {
			validateOpenAPIResponse(t, readCatalogOpenAPI(t), "/api/v1/admin"+strings.Split(path, "?")[0], "get", status, response.Body.Bytes())
		}
	}
	response, _ = f.request(t, "PUT", fmt.Sprintf("/category-mappings/%d", manual.ID), map[string]any{"category_id": *manual.CategoryID})
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, readCatalogOpenAPI(t), "/api/v1/admin/category-mappings/{id}", "put", 200, response.Body.Bytes())
	bearer := f.bearer
	f.bearer = ""
	response, _ = f.request(t, "GET", "/inventories", nil)
	require.Equal(t, 401, response.Code)
	f.bearer = bearer
}

func assertMappedProductFilter(t *testing.T, f *syncFixture, categoryID int64) {
	t.Helper()
	spu := catalog.ProductSPU{TenantID: 1, Title: "Mapped product", Status: "active", Source: "manual", Attributes: []byte(`{}`), Ext: []byte(`{}`)}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Create(&spu).Error)
	mapped := ingestion.Mapped{TenantID: 1, ShopID: f.shop.ID, PlatformID: f.shop.PlatformID, ExternalID: "mapped-product", SourceFetchedAt: time.Now().UTC(),
		Values:      map[string]any{"category_id": "category-01", "currency": "CNY"},
		Collections: map[string][]map[string]any{"skus": {{"external_sku_id": "variant", "stock": 12}}}}
	require.NoError(t, f.database.Transaction(f.ctx, func(ctx context.Context) error {
		tx, stop := f.database.WithContext(ctx)
		defer stop()
		_, err := (catalog.Upserter{}).Upsert(ctx, tx, ingestion.ResourceProduct, []ingestion.Mapped{mapped})
		return err
	}))
	require.NoError(t, q.Table("listing").Where("tenant_id=1 AND external_product_id='mapped-product'").Update("spu_id", spu.ID).Error)
	store := NewProductStore(f.database)
	rows, total, _, err := store.ListProducts(f.ctx, 1, catalog.ProductListQuery{ProductFilter: catalog.ProductFilter{CategoryID: categoryID}, Page: 1, PageSize: 20, Sort: "-updated_at"}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, spu.ID, rows[0].ID)
	detail, err := store.Product(f.ctx, 1, spu.ID)
	require.NoError(t, err)
	require.EqualValues(t, 12, detail.ReportedStock)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	signer, err := catalog.NewProductCursorSigner([]byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	service, err := catalog.NewProductService(store, newAdminStore(f.database).Authorize, signer)
	require.NoError(t, err)
	require.NoError(t, catalog.RegisterProductRoutes(f.router, f.identityFixture.service, profiles, service))
	response, _ := f.request(t, "GET", fmt.Sprintf("/products?category_id=%d", categoryID), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"title":"Mapped product"`)
	response, _ = f.request(t, "GET", fmt.Sprintf("/products/%d", spu.ID), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, readCatalogOpenAPI(t), "/api/v1/admin/products/{id}", "get", 200, response.Body.Bytes())
}
