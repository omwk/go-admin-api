package bootstrap

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationMinIOExportPinsProvider(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	first := f.save(t, f.input, 0, "")
	store := &exportStore{database: f.database}
	objects := &resolvedExportObjects{database: f.database, resolver: f.resolver}
	service, err := catalog.NewExportService(store, objects, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	job, err := service.Create(f.ctx, 1, f.actor.User.ID, catalog.ExportRequest{}, nil)
	require.NoError(t, err)
	require.NoError(t, service.Execute(f.ctx, 1, job.ID))
	persisted, err := store.ReadExport(f.ctx, 1, f.actor.User.ID, job.ID)
	require.NoError(t, err)
	require.NotNil(t, persisted.FileID)
	file, err := newFileStore(f.database).File(f.ctx, 1, *persisted.FileID)
	require.NoError(t, err)
	require.Equal(t, first.ID, *file.ProviderID)
	require.Equal(t, f.input.Bucket, file.Bucket)
	f.input.Name = "export-new-default"
	f.useMinIO(t)
	second := f.save(t, f.input, 0, "")
	status, err := service.Status(f.ctx, 1, f.actor.User.ID, job.ID)
	require.NoError(t, err)
	response := exportDownload(f.ctx, t, status.DownloadURL)
	require.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.True(t, bytes.Contains(body, []byte("id")))
	// Preserve the historical bucket even if this provider's bucket is edited.
	f.input.Bucket = "edited-bucket-" + stringHex(t, 8)
	f.save(t, f.input, first.ID, "")
	w, _ := f.request(t, "POST", "/storage-providers/"+formatStorageID(second.ID)+"/default", nil, "")
	require.Equal(t, 200, w.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("export_job").Where("id=?", job.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	require.NoError(t, store.ExpireExports(f.ctx, 1, job.ID, time.Now()))
	var cleanup outbox.Event
	require.NoError(t, q.Table("outbox_event").Where("event_type='export.cleanup' AND payload->>'key'=?", persisted.ObjectKey).Take(&cleanup).Error)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(cleanup.Payload, &payload))
	require.EqualValues(t, first.ID, payload["provider_id"])
	require.Equal(t, file.Bucket, payload["bucket"])
	// Cleanup can retry after metadata deletion, using its durable binding.
	require.NoError(t, exportEventHandler(service, nil)(f.ctx, cleanup))
	selected, err := f.resolver.Resolve(f.ctx, &first.ID)
	require.NoError(t, err)
	selected.Object = selected.Object.WithBucket(file.Bucket)
	_, err = selected.Object.Get(f.ctx, persisted.ObjectKey)
	require.Error(t, err)
	t.Log("export integration: publication provider_id persisted; default switch preserves signed download; expiry outbox retains historical provider and bucket")
}

func formatStorageID(id int64) string { return strconv.FormatInt(id, 10) }
