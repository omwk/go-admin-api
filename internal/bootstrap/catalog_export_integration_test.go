package bootstrap

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationFrozenContentAndRetry(t *testing.T) {
	// REQ-PRD-008: first worker materialization fixes content across every retry.
	f := newExportFixture(t, 1001)
	request := catalog.ExportRequest{Filters: catalog.ProductFilter{Status: "active"}, Sort: "title"}
	job := f.create(t, request)
	request.Filters.Status = "archived"
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE product_spu SET title='=1+1' WHERE title='frozen-1'"))
	first := f.begin(t, job.ID)
	require.EqualValues(t, 1001, first.RowTotal)
	var snapshot catalog.ExportSnapshot
	require.NoError(t, json.Unmarshal(first.Filters, &snapshot))
	require.Equal(t, "active", snapshot.Filters.Status)
	require.Equal(t, 1, snapshot.FieldsVersion)
	rows, err := f.store.ExportRows(f.ctx, first, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1000)
	require.NotContains(t, string(rows[0].Projection), "_ingestion")
	require.NotContains(t, string(rows[0].Projection), "raw")
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE product_spu SET title='modified-after-start',status='archived',price_min_cents=999"))
	require.NoError(t, f.store.FailExport(f.ctx, first))
	second := f.begin(t, job.ID)
	require.Equal(t, 2, second.Attempt)
	require.NoError(t, f.exports.Upload(f.ctx, second))
	result := f.read(t, job.ID)
	require.Equal(t, "succeeded", result.Status)
	require.Equal(t, second.RowTotal, result.RowCount)
	records := f.records(t, result.ObjectKey)
	require.Len(t, records, 1002)
	require.Equal(t, "'=1+1", records[1][1])
	for _, record := range records[1:] {
		require.NotEqual(t, "modified-after-start", record[1])
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var mismatches int64
	require.NoError(t, q.Raw(`SELECT count(*) FROM export_job_row a JOIN export_job_row b ON a.export_id=b.export_id AND a.ordinal=b.ordinal
WHERE a.export_id=? AND a.attempt=1 AND b.attempt=2 AND (a.projection<>b.projection OR a.generated_at<>b.generated_at)`, job.ID).Scan(&mismatches).Error)
	require.Zero(t, mismatches)
	require.NoError(t, f.exports.Execute(f.ctx, 1, job.ID))
	require.Equal(t, 2, f.read(t, job.ID).Attempt)
}

func TestExportIntegrationHundredThousandRows(t *testing.T) {
	f := newExportFixture(t, 100000)
	job := f.create(t, catalog.ExportRequest{})
	started := time.Now()
	attempt := f.begin(t, job.ID)
	materialized := time.Since(started)
	require.Less(t, materialized, 3*time.Second)
	require.NoError(t, f.exports.Upload(f.ctx, attempt))
	elapsed := time.Since(started)
	require.Less(t, elapsed, 180*time.Second)
	result := f.read(t, job.ID)
	require.EqualValues(t, 100000, result.RowCount)
	records := f.records(t, result.ObjectKey)
	require.Len(t, records, 100001)
	seen := make(map[string]bool, 100000)
	for _, record := range records[1:] {
		require.False(t, seen[record[0]])
		seen[record[0]] = true
	}
	t.Logf("100000 rows: materialize=%s total=%s CSV_records=%d", materialized, elapsed, len(records)-1)
}

func TestExportIntegrationHTTPIsolationIdempotencyAndValidation(t *testing.T) {
	f := newExportFixture(t, 1)
	send := func(input any) (int, []byte) {
		response, _ := shopHTTPRequest(t, f.router, "POST", "/api/v1/admin/products/export", input, f.bearer)
		return response.Code, response.Body.Bytes()
	}
	status, body := send(map[string]any{"filters": map[string]any{}, "object_key": "untrusted"})
	require.Equal(t, 400, status, string(body))
	status, body = send(catalog.ExportRequest{})
	require.Equal(t, 201, status, string(body))
	var envelope struct {
		Data catalog.ExportJob `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	job := envelope.Data
	require.NoError(t, f.exports.Execute(f.ctx, 1, job.ID))
	response, _ := shopHTTPRequest(t, f.router, "GET", fmt.Sprintf("/api/v1/admin/exports/%d", job.ID), nil, f.bearer)
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, readCatalogOpenAPI(t), "/api/v1/admin/exports/{id}", "get", response.Code, response.Body.Bytes())
	require.Contains(t, response.Body.String(), "download_url")
	require.NotContains(t, response.Body.String(), "object_key")
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET tenant_id=2 WHERE id=?", job.ID))
	response, _ = shopHTTPRequest(t, f.router, "GET", fmt.Sprintf("/api/v1/admin/exports/%d", job.ID), nil, f.bearer)
	require.Equal(t, 404, response.Code)
	validateOpenAPIResponse(t, readCatalogOpenAPI(t), "/api/v1/admin/exports/{id}", "get", response.Code, response.Body.Bytes())
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET tenant_id=1,requested_by=? WHERE id=?", f.user.ID, job.ID))
	response, _ = shopHTTPRequest(t, f.router, "GET", fmt.Sprintf("/api/v1/admin/exports/%d", job.ID), nil, f.bearer)
	require.Equal(t, 404, response.Code)
	for _, code := range []string{"=1+1", "+1", "-1", "@SUM(A1)"} {
		require.NoError(t, f.database.Exec(f.ctx, "UPDATE product_spu SET title=?", code))
		formula := f.create(t, catalog.ExportRequest{})
		require.NoError(t, f.exports.Execute(f.ctx, 1, formula.ID))
		require.Equal(t, "'"+code, f.records(t, f.read(t, formula.ID).ObjectKey)[1][1])
	}
	_, err := f.store.ReadExport(f.ctx, 2, f.actor.User.ID, job.ID)
	require.Equal(t, 404, errs.From(err).HTTPStatus)
}

func TestExportIntegrationDigestFailureAndEmptySnapshot(t *testing.T) {
	f := newExportFixture(t, 1)
	f.objects.corrupt = true
	job := f.create(t, catalog.ExportRequest{})
	require.Error(t, f.exports.Execute(f.ctx, 1, job.ID))
	require.Equal(t, "failed", f.read(t, job.ID).Status)
	require.Empty(t, f.read(t, job.ID).ObjectKey)
	f.objects.corrupt = false
	require.NoError(t, f.exports.Execute(f.ctx, 1, job.ID))
	empty := f.create(t, catalog.ExportRequest{Filters: catalog.ProductFilter{Keyword: "absent"}})
	require.NoError(t, f.exports.Execute(f.ctx, 1, empty.ID))
	require.Zero(t, f.read(t, empty.ID).RowCount)
	require.Len(t, f.records(t, f.read(t, empty.ID).ObjectKey), 1)
}

func TestExportIntegrationIdempotency(t *testing.T) {
	f := newExportFixture(t, 0)
	for range 2 {
		response, _ := adminRequest(t, f.adminFixture, "POST", "/products/export", catalog.ExportRequest{}, "export-once")
		require.Equal(t, 201, response.Code)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("export_job").Count(&count).Error)
	require.EqualValues(t, 1, count)
	response, _ := adminRequest(t, f.adminFixture, "POST", "/products/export", catalog.ExportRequest{Sort: "title"}, "export-once")
	require.Equal(t, 409, response.Code)
}
