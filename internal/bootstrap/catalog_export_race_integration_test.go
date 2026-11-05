package bootstrap

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/catalog"
	"github.com/stretchr/testify/require"
)

func TestExportIntegrationConcurrentAndLateAttempts(t *testing.T) {
	for _, finishOldLast := range []bool{false, true} {
		t.Run(map[bool]string{false: "overlapping_uploads", true: "late_old_upload"}[finishOldLast], func(t *testing.T) {
			f := newExportFixture(t, 1001)
			job := f.create(t, catalog.ExportRequest{})
			first := f.begin(t, job.ID)
			entered, release := make(chan struct{}), make(chan struct{})
			f.objects.beforePut = func(ctx context.Context, key string) error {
				if !strings.HasSuffix(key, "attempt-1.csv") {
					return nil
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			oldDone := make(chan error, 1)
			go func() { oldDone <- f.exports.Upload(f.ctx, first) }()
			select {
			case <-entered:
			case <-f.ctx.Done():
				t.Fatal("old upload did not start")
			}
			require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET started_at=now()-interval '6 minutes' WHERE id=?", job.ID))
			second := f.begin(t, job.ID)
			require.Equal(t, 2, second.Attempt)
			if finishOldLast {
				require.NoError(t, f.exports.Upload(f.ctx, second))
				before := f.records(t, catalog.ExportObjectKey(second))
				close(release)
				require.NoError(t, <-oldDone)
				require.Equal(t, before, f.records(t, catalog.ExportObjectKey(second)))
			} else {
				newDone := make(chan error, 1)
				go func() { newDone <- f.exports.Upload(f.ctx, second) }()
				close(release)
				require.NoError(t, <-oldDone)
				require.NoError(t, <-newDone)
			}
			result := f.read(t, job.ID)
			require.Equal(t, "succeeded", result.Status)
			require.Equal(t, catalog.ExportObjectKey(second), result.ObjectKey)
			require.NotNil(t, result.FileID)
			status, err := f.exports.Status(f.ctx, 1, f.actor.User.ID, job.ID)
			require.NoError(t, err)
			require.NotEmpty(t, status.DownloadURL)
			require.Equal(t, result.ObjectKey, f.objects.signed)
			_, err = f.objects.Get(f.ctx, catalog.ExportObjectKey(first))
			require.Error(t, err, "losing attempt must be unreachable")
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var count int64
			require.NoError(t, q.Table("file_object").Where("purpose='export'").Count(&count).Error)
			require.EqualValues(t, 1, count, "losing CAS must roll back file registration")
		})
	}
}

func TestExportIntegrationCleanupProtectsReferencedOldAttempt(t *testing.T) {
	f := newExportFixture(t, 1)
	job := f.create(t, catalog.ExportRequest{})
	first := f.begin(t, job.ID)
	require.NoError(t, f.exports.Upload(f.ctx, first))
	referenced := f.read(t, job.ID).ObjectKey
	second := first
	second.Attempt = 2
	extra := catalog.ExportObjectKey(second)
	_, err := f.objects.Put(f.ctx, extra, strings.NewReader("unpublished"), -1)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE export_job SET attempt=2 WHERE id=?", job.ID))
	require.NoError(t, f.exports.Cleanup(f.ctx, 1, job.ID))
	require.NotEmpty(t, f.records(t, referenced))
	_, err = f.objects.Get(f.ctx, extra)
	require.Error(t, err)
	require.Equal(t, referenced, f.read(t, job.ID).ObjectKey)
}

func TestExportIntegrationCleanupOrphansAgeAndRetry(t *testing.T) {
	f := newExportFixture(t, 0)
	job := f.create(t, catalog.ExportRequest{})
	require.NoError(t, f.exports.Execute(f.ctx, 1, job.ID))
	old := catalog.ExportObjectKey(catalog.ExportJob{TenantID: 1, ID: job.ID, Attempt: 2})
	fresh := strings.Replace(old, "attempt-2.csv", "tmp-fresh", 1)
	temporary := strings.Replace(old, "attempt-2.csv", "tmp-old", 1)
	unrelated := "product-images/retained"
	for _, key := range []string{old, fresh, temporary, unrelated} {
		_, err := f.objects.Put(f.ctx, key, strings.NewReader("temporary"), -1)
		require.NoError(t, err)
		f.objects.dates[key] = time.Now().Add(-2 * time.Hour)
	}
	f.objects.dates[fresh] = time.Now()
	f.objects.deleteError = true
	require.Error(t, f.exports.Cleanup(f.ctx, 0, 0))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var pending int64
	// The scanner order is deliberately unspecified; at least one exact cleanup obligation persists.
	require.NoError(t, q.Table("outbox_event").Where("event_type='export.cleanup' AND payload ? 'key'").Count(&pending).Error)
	require.Positive(t, pending)
	for range 2 {
		require.Error(t, f.exports.RetryCleanup(f.ctx, 1, job.ID, old))
	}
	var afterRetries int64
	require.NoError(t, q.Table("outbox_event").Where("event_type='export.cleanup' AND payload ? 'key'").Count(&afterRetries).Error)
	require.Equal(t, pending, afterRetries, "outbox retries must not multiply cleanup obligations")
	f.objects.deleteError = false
	require.NoError(t, f.exports.Cleanup(f.ctx, 0, 0))
	for _, key := range []string{old, temporary} {
		_, err := f.objects.Get(f.ctx, key)
		require.Error(t, err)
	}
	for _, key := range []string{fresh, unrelated} {
		object, err := f.objects.Get(f.ctx, key)
		require.NoError(t, err)
		require.NoError(t, object.Body.Close())
	}
}
