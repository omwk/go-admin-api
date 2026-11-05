package bootstrap

import (
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationAdmissionConcurrent(t *testing.T) {
	// REQ-PRD-008: creation reserves capacity under the database global lock.
	f := newExportFixture(t, 0)
	start := make(chan struct{})
	type result struct {
		job catalog.ExportJob
		err error
	}
	results := make(chan result, 10)
	var group sync.WaitGroup
	for i := range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			job, err := f.store.CreateExport(f.ctx, int64(i/2+1), int64(i+100), catalog.ExportSnapshot{FieldsVersion: 1}, nil)
			results <- result{job, err}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	accepted, rejected := 0, 0
	tenants := map[int64]bool{}
	for result := range results {
		if result.err != nil {
			require.Equal(t, 429, errs.From(result.err).HTTPStatus)
			require.Equal(t, 10005, errs.From(result.err).Code)
			rejected++
			continue
		}
		require.Equal(t, "running", result.job.Status)
		require.False(t, tenants[result.job.TenantID])
		tenants[result.job.TenantID] = true
		accepted++
	}
	require.Equal(t, 2, accepted)
	require.Equal(t, 8, rejected)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("export_job").Where("status='running'").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.NoError(t, q.Table("outbox_event").Where("event_type='export.dispatch'").Count(&count).Error)
	require.EqualValues(t, 2, count, "rejected transactions create neither jobs nor dispatch events")
}

func TestExportIntegrationAdmissionLayers(t *testing.T) {
	for _, layer := range []string{"tenant_pending", "requester_running", "tenant_running", "global_running"} {
		t.Run(layer, func(t *testing.T) {
			f := newExportFixture(t, 0)
			user := f.actor.User.ID
			switch layer {
			case "tenant_pending":
				require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO export_job(tenant_id,kind,status,requested_by,object_key) SELECT 1,'products','pending',?,'' FROM generate_series(1,10)`, user))
			case "requester_running":
				f.create(t, catalog.ExportRequest{})
			case "tenant_running":
				require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO export_job(tenant_id,kind,status,requested_by,object_key) VALUES(1,'products','running',?,'')`, user+1))
			case "global_running":
				require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO export_job(tenant_id,kind,status,requested_by,object_key) VALUES(2,'products','running',?,''), (3,'products','running',?,'')`, user+1, user+2))
			}
			response, _ := adminRequest(t, f.adminFixture, "POST", "/products/export", catalog.ExportRequest{}, "limited-request")
			require.Equal(t, 429, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), `"code":10005`)
			require.Contains(t, response.Body.String(), layer)
			validateOpenAPIResponse(t, f.contract, "/api/v1/admin/products/export", "post", 429, response.Body.Bytes())
		})
	}
}

func TestExportIntegrationAdmissionRetryAndRedisFailure(t *testing.T) {
	f := newExportFixture(t, 0)
	job := f.create(t, catalog.ExportRequest{})
	first := f.begin(t, job.ID)
	require.NoError(t, f.store.FailExport(f.ctx, first))
	other := f.create(t, catalog.ExportRequest{})
	_, err := f.store.BeginExport(f.ctx, 1, job.ID)
	require.Equal(t, 429, errs.From(err).HTTPStatus, "failed attempts must reacquire capacity")
	require.NoError(t, f.exports.Execute(f.ctx, 1, other.ID))
	require.NoError(t, f.exports.Execute(f.ctx, 1, job.ID))
	f.server.Close()
	// The storage adapter has no Redis fallback: DB running rows always deny admission.
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO export_job(tenant_id,kind,status,requested_by,object_key) VALUES(1,'products','running',?,'')`, f.actor.User.ID))
	_, err = f.store.CreateExport(f.ctx, 1, f.actor.User.ID, catalog.ExportSnapshot{FieldsVersion: 1}, nil)
	require.Equal(t, 10005, errs.From(err).Code)
	response, _ := adminRequest(t, f.adminFixture, "POST", "/products/export", catalog.ExportRequest{}, "")
	require.Equal(t, 503, response.Code, "Redis authorization outage cannot admit HTTP work")
}
