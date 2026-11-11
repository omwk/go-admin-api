package bootstrap

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func catalogContractFixture(t *testing.T) *credentialFixture {
	t.Helper()
	f := newCredentialFixture(t)
	signer, err := catalog.NewProductCursorSigner(shopRandomBytes(t, 32))
	require.NoError(t, err)
	service, err := catalog.NewProductService(NewProductStore(f.database), newAdminStore(f.database).Authorize, signer)
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, catalog.RegisterProductRoutes(f.router, f.identityFixture.service, profiles, service))
	return f
}

func TestCatalogContractIntegrationDuplicateProductError(t *testing.T) {
	// BE-4 §2: exercise a real PostgreSQL unique violation through authenticated HTTP.
	f := catalogContractFixture(t)
	input := map[string]any{"code": "be4-duplicate", "title": "x", "status": "draft"}
	w, _ := f.request(t, http.MethodPost, "/products", input)
	require.Equal(t, 201, w.Code, w.Body.String())
	w, _ = f.request(t, http.MethodPost, "/products", input)
	require.Contains(t, []int{409, 500}, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotZero(t, body["code"])
	trace := w.Header().Get("X-Request-Id")
	require.NotEmpty(t, trace)
	require.Equal(t, trace, body["trace_id"])
	for _, private := range []string{"SQL", "INSERT", "product_spu", "constraint", "duplicate key"} {
		require.NotContains(t, w.Body.String(), private)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(f.logs.text()), "\n") {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		if record["trace_id"] == trace && record["error"] != nil {
			require.Contains(t, record["error"], "duplicate key value violates unique constraint")
			require.EqualValues(t, 10002, record["error_code"])
			found = true
		}
	}
	require.True(t, found, "original PostgreSQL error must be logged with the response trace")
	t.Logf("duplicate product: status=%d body=%s; matching raw-error log found", w.Code, w.Body.String())
}

func TestCatalogContractIntegrationProductListCategoryJoin(t *testing.T) {
	f := catalogContractFixture(t)
	categoryID := seedCatalogContractProducts(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var categoryQueries, productQueries atomic.Int32
	callback := q.Callback().Query()
	require.NoError(t, callback.After("gorm:query").Register("be4:category_count", func(query *gorm.DB) {
		sql := strings.ToLower(query.Statement.SQL.String())
		if strings.Contains(sql, "category") {
			categoryQueries.Add(1)
		}
		if strings.Contains(sql, "from product_spu p") {
			productQueries.Add(1)
		}
	}))
	t.Cleanup(func() { require.NoError(t, callback.Remove("be4:category_count")) })
	w, envelope := f.request(t, http.MethodGet, "/products?page_size=20", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var page struct {
		List  []map[string]any `json:"list"`
		Total int64            `json:"total"`
	}
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 20, page.Total)
	require.Len(t, page.List, 20)
	for _, item := range page.List {
		require.Contains(t, item, "category_id")
		require.Contains(t, item, "category_name")
		id := int(item["id"].(float64))
		if id >= 18 {
			require.Nil(t, item["category_id"], "product %d: absent, deleted or foreign category", id)
			require.Nil(t, item["category_name"])
		} else {
			require.EqualValues(t, categoryID, item["category_id"])
			require.Equal(t, "电脑外设", item["category_name"])
		}
		if id == 7 {
			t.Logf("product 7: category_id=%v category_name=%s", item["category_id"], item["category_name"])
		}
	}
	require.EqualValues(t, 1, categoryQueries.Load(), "one category JOIN for all 20 items")
	require.EqualValues(t, 2, productQueries.Load(), "one count and one page query")
	t.Logf("20 items: category SQL=%d, product SQL=%d; products 18/19/20 have null category fields", categoryQueries.Load(), productQueries.Load())
}

func seedCatalogContractProducts(t *testing.T, f *credentialFixture) int64 {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	category := catalog.Category{TenantID: 1, Name: "电脑外设", Path: "1.6", Level: 2, Status: "active"}
	deleted := catalog.Category{TenantID: 1, Name: "Removed category", Path: "1.7", Level: 2, Status: "active"}
	foreign := catalog.Category{TenantID: 2, Name: "Private category", Path: "2.8", Level: 2, Status: "active"}
	for _, row := range []*catalog.Category{&category, &deleted, &foreign} {
		require.NoError(t, q.Create(row).Error)
	}
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO product_spu(id,tenant_id,title,status,source,category_id) OVERRIDING SYSTEM VALUE
SELECT n,1,'be4-product-'||n,'draft','manual',CASE WHEN n=18 THEN NULL::bigint WHEN n=19 THEN ?::bigint WHEN n=20 THEN ?::bigint ELSE ?::bigint END
FROM generate_series(1,20) n`, deleted.ID, foreign.ID, category.ID))
	require.NoError(t, f.database.Exec(f.ctx, `SELECT setval(pg_get_serial_sequence('product_spu','id'),20,true)`))
	require.NoError(t, q.Where("id=?", deleted.ID).Delete(&catalog.Category{}).Error)
	return category.ID
}
