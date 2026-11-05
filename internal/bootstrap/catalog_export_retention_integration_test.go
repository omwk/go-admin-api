package bootstrap

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationRetentionAndDeleteRetry(t *testing.T) {
	// REQ-PRD-008: a 25h object expires, while a fresh published object survives.
	f := newExportFixture(t, 1)
	expired := f.create(t, catalog.ExportRequest{})
	require.NoError(t, f.exports.Execute(f.ctx, 1, expired.ID))
	key := f.read(t, expired.ID).ObjectKey
	f.objects.dates[key] = time.Now().Add(-25 * time.Hour)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE export_job SET finished_at=now()-interval '25 hours', expires_at=now()-interval '1 hour' WHERE id=?`, expired.ID))
	fresh := f.create(t, catalog.ExportRequest{})
	require.NoError(t, f.exports.Execute(f.ctx, 1, fresh.ID))
	f.objects.deleteError = true
	require.Error(t, f.exports.Cleanup(f.ctx, 0, 0))
	job := f.read(t, expired.ID)
	require.Equal(t, "expired", job.Status)
	require.Empty(t, job.ObjectKey)
	require.Nil(t, job.FileID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var pending int64
	require.NoError(t, q.Table("outbox_event").Where("event_type='export.cleanup' AND payload @> jsonb_build_object('key',?::text)", key).Count(&pending).Error)
	require.EqualValues(t, 1, pending)
	require.Error(t, f.exports.Cleanup(f.ctx, 0, 0))
	f.objects.deleteError = false
	require.NoError(t, f.exports.RetryCleanup(f.ctx, 1, expired.ID, key))
	_, err := f.objects.Get(f.ctx, key)
	require.Error(t, err)
	require.NoError(t, f.exports.Cleanup(f.ctx, 0, 0))
	require.Equal(t, "succeeded", f.read(t, fresh.ID).Status)
	require.NotEmpty(t, f.records(t, f.read(t, fresh.ID).ObjectKey))
	require.NoError(t, f.exports.Execute(f.ctx, 1, expired.ID))
	require.Equal(t, "expired", f.read(t, expired.ID).Status)
}

type exportScanHook struct {
	*exportMemory
	beforeVisit func() error
}

func (m *exportScanHook) Scan(ctx context.Context, prefix string, visit func(port.ObjectInfo) error) error {
	return m.exportMemory.Scan(ctx, prefix, func(info port.ObjectInfo) error {
		if m.beforeVisit != nil {
			if err := m.beforeVisit(); err != nil {
				return err
			}
			m.beforeVisit = nil
		}
		return visit(info)
	})
}

func TestExportIntegrationCleanupRereadsPublication(t *testing.T) {
	f := newExportFixture(t, 0)
	job := f.create(t, catalog.ExportRequest{})
	attempt := f.begin(t, job.ID)
	key := catalog.ExportObjectKey(attempt)
	_, err := f.objects.Put(f.ctx, key, strings.NewReader("uploaded"), -1)
	require.NoError(t, err)
	f.objects.dates[key] = time.Now().Add(-2 * time.Hour)
	hook := &exportScanHook{exportMemory: f.objects, beforeVisit: func() error {
		published, err := f.store.PublishExport(f.ctx, attempt, key, catalog.ExportDigest{Size: 8, Rows: 0, SHA256: strings.Repeat("a", 64)})
		require.True(t, published)
		return err
	}}
	service, err := catalog.NewExportService(f.store, hook, newAdminStore(f.database).Authorize)
	require.NoError(t, err)
	require.NoError(t, service.Cleanup(f.ctx, 0, 0))
	require.Equal(t, key, f.read(t, job.ID).ObjectKey)
	object, err := f.objects.Get(f.ctx, key)
	require.NoError(t, err)
	require.NoError(t, object.Body.Close())
}
