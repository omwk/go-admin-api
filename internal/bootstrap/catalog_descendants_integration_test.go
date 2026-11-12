package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedCatalogDescendants(t *testing.T, f *credentialFixture) {
	t.Helper()
	// BE-5: eight products each in 1, 6 and 7; sibling, foreign and absent
	// categories must not enter the ancestor result.
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO category(id,tenant_id,parent_id,name,path,level)
OVERRIDING SYSTEM VALUE VALUES (1,1,0,'Root','1',1),(6,1,1,'Child six','1.6',2),
(7,1,1,'Leaf seven','1.7',2),(9,1,6,'Grandchild','1.6.9',3),
(11,1,0,'Sibling','11',1),(12,2,1,'Foreign child','1.12',2)`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO product_spu(tenant_id,title,status,source,category_id)
SELECT 1,'be5-'||c||'-'||n,'active','manual',c FROM unnest(ARRAY[1,6,7]) c CROSS JOIN generate_series(1,8) n`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO product_spu(tenant_id,title,status,source,category_id)
VALUES (1,'Sibling','active','manual',11),(1,'Foreign category','active','manual',12),
(1,'Uncategorized','active','manual',NULL),(2,'Foreign product','active','manual',6)`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO listing(tenant_id,shop_id,platform_id,spu_id,external_product_id,title,price_cents,currency,status)
SELECT 1,?,?,id,'be5-'||id,title,0,'CNY','online' FROM product_spu WHERE tenant_id=1`, f.shop.ID, f.shop.PlatformID))
}

func TestCatalogDescendantsIntegrationLists(t *testing.T) {
	f := catalogContractFixture(t)
	seedCatalogDescendants(t, f)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var queries, categoryLookups atomic.Int32
	callback := q.Callback().Query()
	require.NoError(t, callback.After("gorm:query").Register("be5:query_count", func(query *gorm.DB) {
		if query.DryRun {
			return // GORM builds nested subqueries without executing them.
		}
		sql := strings.ToLower(query.Statement.SQL.String())
		if strings.Contains(sql, "from product_spu p") || strings.Contains(sql, `from "listing"`) {
			queries.Add(1)
			require.NotContains(t, sql, "with recursive")
		}
		if strings.Contains(sql, `from "category"`) || strings.Contains(sql, "from category ") {
			if !strings.Contains(sql, "from product_spu p") && !strings.Contains(sql, `from "listing"`) {
				categoryLookups.Add(1)
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, callback.Remove("be5:query_count")) })
	for _, endpoint := range []string{"/products", "/listings"} {
		for _, tc := range []struct {
			name, filter string
			total        int64
		}{
			{"default", "category_id=1", 8},
			{"false", "category_id=1&include_descendants=false", 8},
			{"descendants", "category_id=1&include_descendants=true", 24},
			{"leaf_exact", "category_id=7", 8},
			{"leaf_descendants", "category_id=7&include_descendants=true", 8},
			{"unfiltered", "", 27},
			{"no_category", "include_descendants=true", 27},
			{"unknown_category", "category_id=999&include_descendants=true", 0},
			{"foreign_root", "category_id=12&include_descendants=true", 1},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				queries.Store(0)
				categoryLookups.Store(0)
				ids := catalogDescendantPage(t, f, endpoint, tc.filter, tc.total)
				require.Len(t, ids, int(tc.total))
				require.EqualValues(t, 2, queries.Load(), "one count and one page, with no category lookup per row")
				require.Zero(t, categoryLookups.Load(), "category matching stays in the list SQL")
				t.Logf("%s?%s: total=%d SQL=%d", endpoint, tc.filter, tc.total, queries.Load())
			})
		}
	}
	for _, endpoint := range []string{"/products", "/listings"} {
		for _, invalid := range []string{"wrong", "", "1", "null"} {
			response, envelope := f.request(t, http.MethodGet, endpoint+"?include_descendants="+invalid, nil)
			require.Equal(t, 400, response.Code, response.Body.String())
			require.Equal(t, 10001, envelope.Code)
		}
	}
}

func catalogDescendantPage(t *testing.T, f *credentialFixture, endpoint, filter string, total int64) []string {
	t.Helper()
	response, envelope := f.request(t, http.MethodGet, endpoint+"?page_size=100&"+filter, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var page struct {
		List  []struct{ ID int64 } `json:"list"`
		Total int64                `json:"total"`
	}
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Equal(t, total, page.Total)
	ids := make([]string, 0, len(page.List))
	for _, item := range page.List {
		ids = append(ids, strconv.FormatInt(item.ID, 10))
	}
	return ids
}

func TestCatalogDescendantsIntegrationMappingsAndNestedPath(t *testing.T) {
	f := catalogContractFixture(t)
	seedCatalogDescendants(t, f)
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO category(id,tenant_id,parent_id,name,path,level)
OVERRIDING SYSTEM VALUE VALUES (60,1,1,'Prefix sibling','1.60',2)`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO product_spu(tenant_id,title,status,source,category_id)
VALUES (1,'Nested','active','manual',9),(1,'Prefix sibling','active','manual',60),
(1,'Mapped','active','manual',NULL)`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO platform_category(tenant_id,platform_id,external_id,name,path)
VALUES (1,?,'be5-mapped','Mapped leaf','mapped')`, f.shop.PlatformID))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO category_mapping(tenant_id,platform_id,platform_category_id,category_id,source)
SELECT 1,platform_id,id,6,'manual' FROM platform_category WHERE external_id='be5-mapped'`))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO listing(tenant_id,shop_id,platform_id,spu_id,external_product_id,title,price_cents,currency,status,ext)
SELECT 1,?,?,id,'extra-'||id,title,0,'CNY','online',
CASE WHEN title='Mapped' THEN '{"category_id":"be5-mapped"}'::jsonb ELSE '{}'::jsonb END
FROM product_spu WHERE title IN ('Nested','Prefix sibling','Mapped')`, f.shop.ID, f.shop.PlatformID))
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO listing(tenant_id,shop_id,platform_id,external_product_id,title,price_cents,currency,status,ext)
VALUES (1,?,?,'unbound','Unbound mapped',0,'CNY','online','{"category_id":"be5-mapped"}')`, f.shop.ID, f.shop.PlatformID))
	for _, tc := range []struct {
		endpoint, filter string
		total            int64
	}{
		{"/products", "category_id=6", 9},
		{"/products", "category_id=6&include_descendants=true", 10},
		{"/listings", "category_id=6", 10},
		{"/listings", "category_id=6&include_descendants=true", 11},
	} {
		catalogDescendantPage(t, f, tc.endpoint, tc.filter, tc.total)
		t.Logf("%s?%s: total=%d; nested path and applied mappings included, 1.60 excluded", tc.endpoint, tc.filter, tc.total)
	}
}

func TestExportDescendantsIntegrationSnapshotAndCSV(t *testing.T) {
	f := catalogContractFixture(t)
	seedCatalogDescendants(t, f)
	exports := &exportFixture{adminFixture: f.adminFixture, store: &exportStore{database: f.database, bucket: "export-tests"}, objects: newExportMemory()}
	var err error
	exports.exports, err = catalog.NewExportService(exports.store, exports.objects, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(f.database))
	require.NoError(t, err)
	require.NoError(t, catalog.RegisterExportRoutes(f.router, f.identityFixture.service, profiles, exports.exports))
	for _, tc := range []struct {
		name       string
		value      any
		categoryID int64
		total      int64
	}{
		{"default", nil, 1, 8}, {"false", false, 1, 8}, {"descendants", true, 1, 24},
		{"no_category", true, 0, 27},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filters := map[string]any{}
			if tc.categoryID > 0 {
				filters["category_id"] = tc.categoryID
			}
			if tc.value != nil {
				filters["include_descendants"] = tc.value
			}
			response, envelope := f.request(t, http.MethodPost, "/products/export", map[string]any{"filters": filters})
			require.Equal(t, 201, response.Code, response.Body.String())
			var job catalog.ExportJob
			require.NoError(t, json.Unmarshal(envelope.Data, &job))
			stored := exports.read(t, job.ID)
			var snapshot struct{ Filters map[string]any }
			require.NoError(t, json.Unmarshal(stored.Filters, &snapshot))
			require.Equal(t, tc.value == true && tc.categoryID > 0, snapshot.Filters["include_descendants"] == true)
			if tc.categoryID > 0 {
				require.EqualValues(t, tc.categoryID, snapshot.Filters["category_id"])
			}
			require.NoError(t, exports.exports.Execute(f.ctx, 1, job.ID))
			finished := exports.read(t, job.ID)
			require.Equal(t, "succeeded", finished.Status)
			require.Equal(t, tc.total, finished.RowTotal)
			require.Equal(t, tc.total, finished.RowCount)
			records := exports.records(t, finished.ObjectKey)
			require.Len(t, records, int(tc.total)+1)
			ids := make([]string, 0, len(records)-1)
			for _, record := range records[1:] {
				ids = append(ids, record[0])
			}
			filter := fmt.Sprintf("include_descendants=%t", tc.value == true)
			if tc.categoryID > 0 {
				filter += fmt.Sprintf("&category_id=%d", tc.categoryID)
			}
			listIDs := catalogDescendantPage(t, f, "/products", filter, tc.total)
			require.ElementsMatch(t, listIDs, ids)
			t.Logf("snapshot include_descendants=%v; list=%d materialized=%d CSV=%d", snapshot.Filters["include_descendants"], len(listIDs), finished.RowTotal, len(records)-1)
		})
	}
	for _, invalid := range []any{"true", 1, []bool{true}, nil} {
		response, envelope := f.request(t, http.MethodPost, "/products/export", map[string]any{"filters": map[string]any{"include_descendants": invalid}})
		require.Equal(t, 400, response.Code, response.Body.String())
		require.Equal(t, 10001, envelope.Code)
	}
}
