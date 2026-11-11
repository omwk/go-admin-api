package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestExportF2IntegrationProductListOrderPaginationAndOwner(t *testing.T) {
	// F2 §4.6/8: sort precedes pagination and owner/kind restrictions apply to totals.
	f := newExportFixture(t, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	base := time.Now().UTC().Truncate(time.Second)
	jobs := []catalog.ExportJob{}
	for i := 0; i < 23; i++ {
		job := catalog.ExportJob{TenantID: 1, RequestedBy: f.actor.User.ID, Kind: "products", Status: "succeeded", Filters: json.RawMessage(`{}`), CreatedAt: base.Add(time.Duration(i) * time.Second)}
		require.NoError(t, q.Create(&job).Error)
		jobs = append(jobs, job)
	}
	for _, row := range []catalog.ExportJob{
		{TenantID: 2, RequestedBy: f.actor.User.ID, Kind: "products", Status: "succeeded", Filters: json.RawMessage(`{}`)},
		{TenantID: 1, RequestedBy: f.user.ID, Kind: "products", Status: "succeeded", Filters: json.RawMessage(`{}`)},
		{TenantID: 1, RequestedBy: f.actor.User.ID, Kind: "orders", Status: "succeeded", Filters: json.RawMessage(`{}`)},
	} {
		require.NoError(t, q.Create(&row).Error)
	}
	for _, tc := range []struct {
		query                    string
		page, size, count, first int
	}{{"", 1, 20, 20, 22}, {"?page=2", 2, 20, 3, 2}, {"?page_size=200", 1, 100, 23, 22}, {"?page=3", 3, 20, 0, 0}} {
		response, envelope := shopHTTPRequest(t, f.router, "GET", "/api/v1/admin/exports"+tc.query, nil, f.bearer)
		require.Equal(t, 200, response.Code, response.Body.String())
		validateOpenAPIResponse(t, f2ReadContract(t, "catalog.yaml"), "/api/v1/admin/exports", "get", 200, response.Body.Bytes())
		var page catalog.ExportPage
		require.NoError(t, json.Unmarshal(envelope.Data, &page))
		require.EqualValues(t, 23, page.Total)
		require.Equal(t, tc.page, page.Page)
		require.Equal(t, tc.size, page.PageSize)
		require.Len(t, page.List, tc.count)
		if tc.count > 0 {
			require.Equal(t, jobs[tc.first].ID, page.List[0].ID)
			for i := 1; i < len(page.List); i++ {
				require.True(t, page.List[i-1].CreatedAt.After(page.List[i].CreatedAt))
			}
		}
		for _, field := range []string{"object_key", "filters", "download_url", "requested_by", "tenant_id"} {
			require.NotContains(t, string(envelope.Data), `"`+field+`"`)
		}
	}
	for _, query := range []string{"?page=0", "?page_size=0", "?page_size=bad", "?page=10000&page_size=100", "?sort=created_at"} {
		response, _ := shopHTTPRequest(t, f.router, "GET", "/api/v1/admin/exports"+query, nil, f.bearer)
		require.Equal(t, 400, response.Code)
	}
	require.NoError(t, q.Where("tenant_id=1 AND requested_by=? AND kind='products'", f.actor.User.ID).Delete(&catalog.ExportJob{}).Error)
	response, envelope := shopHTTPRequest(t, f.router, "GET", "/api/v1/admin/exports", nil, f.bearer)
	require.Equal(t, 200, response.Code)
	require.JSONEq(t, `{"list":[],"total":0,"page":1,"page_size":20}`, string(envelope.Data))
}

func TestExportF2IntegrationOrderListUsesFrozenShopScope(t *testing.T) {
	// F2 §4.4/6: inaccessible frozen shops are excluded before count and pagination.
	f := newOrderExportFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	jobs := []catalog.ExportJob{}
	for i := 0; i < 3; i++ {
		job := f.create(t)
		require.NoError(t, q.Model(&catalog.ExportJob{}).Where("id=?", job.ID).Updates(map[string]any{
			"status": "succeeded", "created_at": time.Unix(1800000000+int64(i), 0).UTC(),
		}).Error)
		jobs = append(jobs, job)
	}
	response, envelope := f.request(t, "GET", "/orders/exports?page_size=2", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, f2ReadContract(t, "order.yaml"), "/api/v1/admin/orders/exports", "get", 200, response.Body.Bytes())
	var page catalog.ExportPage
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 3, page.Total)
	require.Len(t, page.List, 2)
	require.Equal(t, jobs[2].ID, page.List[0].ID)
	require.Equal(t, jobs[1].ID, page.List[1].ID)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "custom").Error)
	response, envelope = f.request(t, "GET", "/orders/exports", nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.JSONEq(t, `{"list":[],"total":0,"page":1,"page_size":20}`, string(envelope.Data))
}
