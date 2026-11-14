package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/sync/repo"
	"github.com/stretchr/testify/require"
)

func TestSyncF2IntegrationJobsPaginationAndCheckpoint(t *testing.T) {
	// F2 §4.5/8: page/count, empty arrays and checkpoint detail match OpenAPI.
	f := newSyncFixture(t, nil)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for i := 0; i < 22; i++ {
		require.NoError(t, q.Table("sync_job").Create(map[string]any{
			"tenant_id": 1, "shop_id": f.shop.ID, "resource_type": fmt.Sprintf("f2-%02d", i), "cron": "0 * * * *",
		}).Error)
	}
	for _, tc := range []struct {
		query             string
		page, size, count int
	}{{"", 1, 20, 20}, {"?page=2&page_size=20", 2, 20, 3}, {"?page_size=200", 1, 100, 23}, {"?page=3", 3, 20, 0}} {
		response, envelope := f.request(t, "GET", "/sync-jobs"+tc.query, nil)
		require.Equal(t, 200, response.Code, response.Body.String())
		validateOpenAPIResponse(t, f2ReadContract(t, "sync.yaml"), "/api/v1/admin/sync-jobs", "get", 200, response.Body.Bytes())
		var page struct {
			List     []repo.JobView `json:"list"`
			Total    int64          `json:"total"`
			Page     int            `json:"page"`
			PageSize int            `json:"page_size"`
		}
		require.NoError(t, json.Unmarshal(envelope.Data, &page))
		require.Len(t, page.List, tc.count)
		require.EqualValues(t, 23, page.Total)
		require.Equal(t, tc.page, page.Page)
		require.Equal(t, tc.size, page.PageSize)
	}
	require.NoError(t, q.Table("sync_cursor").Create(map[string]any{"tenant_id": 1, "shop_id": f.shop.ID, "resource_type": "product", "cursor_type": "id_watermark", "cursor_value": "42"}).Error)
	response, envelope := f.request(t, "GET", resourcePath("sync-jobs", f.jobID), nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, f2ReadContract(t, "sync.yaml"), "/api/v1/admin/sync-jobs/{id}", "get", 200, response.Body.Bytes())
	var job repo.JobView
	require.NoError(t, json.Unmarshal(envelope.Data, &job))
	require.Equal(t, "0 0 1 1 *", job.Cron)
	require.True(t, job.Enabled)
	require.Equal(t, "42", job.CursorValue)
	for _, query := range []string{"?page=0", "?page_size=bad", "?page=10000&page_size=100", "?sort=unsafe"} {
		response, _ := f.request(t, "GET", "/sync-jobs"+query, nil)
		require.Equal(t, 400, response.Code)
	}
	require.NoError(t, q.Exec("DELETE FROM sync_job WHERE tenant_id=1").Error)
	response, envelope = f.request(t, "GET", "/sync-jobs", nil)
	require.Equal(t, 200, response.Code)
	require.JSONEq(t, `{"list":[],"total":0,"page":1,"page_size":20}`, string(envelope.Data))
}

func TestSyncF2IntegrationJobsOutsideScopeAreNotFound(t *testing.T) {
	// F2 §4.4: both list and detail enforce live shop scope.
	f := newSyncFixture(t, nil)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	foreign := map[string]any{"tenant_id": 2, "shop_id": f.shop.ID, "resource_type": "product", "cron": "0 * * * *"}
	require.NoError(t, q.Table("sync_job").Create(foreign).Error)
	var foreignID int64
	require.NoError(t, q.Table("sync_job").Select("id").Where("tenant_id=2").Take(&foreignID).Error)
	response, _ := f.request(t, "GET", resourcePath("sync-jobs", foreignID), nil)
	require.Equal(t, 404, response.Code)
	ordinaryScopeActor(t, f.identityFixture, f.actor.UserID)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id=1 AND code=?", identity.SuperAdminRoleCode).Update("data_scope", "custom").Error)
	response, _ = f.request(t, "GET", resourcePath("sync-jobs", f.jobID), nil)
	require.Equal(t, 404, response.Code)
	response, envelope := f.request(t, "GET", "/sync-jobs", nil)
	require.Equal(t, 200, response.Code)
	require.JSONEq(t, `{"list":[],"total":0,"page":1,"page_size":20}`, string(envelope.Data))
}
