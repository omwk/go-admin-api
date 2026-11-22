package bootstrap

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/worker"
	"github.com/stretchr/testify/require"
)

func TestFileThumbnailIntegrationUploadGenerateAndFailure(t *testing.T) {
	// BE-IDN-028 §4.1/4/6/8: exercise real HTTP, PostgreSQL and object storage.
	f := newThumbnailFixture(t)
	w, view := f.upload(t, "small.png", "attachment", fileModelImage(t, "png"), "")
	require.Equal(t, 201, w.Code)
	require.Equal(t, "ready", view.ThumbStatus)
	require.NotNil(t, view.ThumbURL)
	t.Logf("#1 POST small: HTTP %d %s", w.Code, w.Body.Bytes())
	large := thumbnailPixelImage(t, 2001, 2001)
	w, view = f.upload(t, "pixel-large.png", "attachment", large, "")
	require.Equal(t, 201, w.Code)
	require.Equal(t, "pending", view.ThumbStatus)
	require.Nil(t, view.ThumbURL)
	assertThumbnailOutbox(t, f, view.ID, 1)
	thumbnails, err := filemodule.NewThumbnailWorker(newFileStore(f.database), fileResolver(f.resolver))
	require.NoError(t, err)
	require.NoError(t, thumbnails.Generate(f.ctx, 1, view.ID))
	ready := assertThumbnailReady(t, f, view.ID)
	require.Equal(t, "ready", ready.ThumbStatus)
	t.Log("#6 >4M pixels and <40M pixels: pending -> ready; GET preview=200 size<=320")
	broken := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("x"), 5<<20)...)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"broken.jpg", broken},
		{"too-many-pixels.png", thumbnailPNGDimensions(t, 10000, 4001)},
	} {
		w, view = f.upload(t, test.name, "attachment", test.data, "")
		require.Equal(t, 201, w.Code)
		require.Equal(t, "pending", view.ThumbStatus)
		require.NoError(t, thumbnails.Generate(f.ctx, 1, view.ID))
		require.NoError(t, thumbnails.Generate(f.ctx, 1, view.ID))
		row, err := newFileStore(f.database).File(f.ctx, 1, view.ID)
		require.NoError(t, err)
		require.Equal(t, "failed", row.ThumbStatus)
		require.Zero(t, row.ThumbAttempts)
		assertThumbnailOutbox(t, f, view.ID, 1)
		t.Logf("#4/#6 %s: failed; repeated delivery no-op; thumb_attempts=0", test.name)
	}
	w, _ = fileModelAvatarUpload(t, f, "pixels.png", large, "")
	require.Equal(t, 400, w.Code)
	t.Logf("#8 avatar >4M pixels: HTTP %d %s; no asynchronous record", w.Code, w.Body.Bytes())
}

func TestFileThumbnailIntegrationFallbackBoundedAndDispatch(t *testing.T) {
	// BE-IDN-028 §3.5: retries are atomic, bounded, tenant scoped and hourly.
	f := newThumbnailFixture(t)
	w, view := f.upload(t, "fallback.png", "attachment", thumbnailPixelImage(t, 2001, 2001), "")
	require.Equal(t, 201, w.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("UPDATE file_object SET created_at=? WHERE id=?", time.Now().Add(-20*time.Minute), view.ID).Error)
	for attempt := 1; attempt <= 4; attempt++ {
		require.NoError(t, q.Exec("UPDATE outbox_event SET created_at=? WHERE event_type='thumb.generate'", time.Now().Add(-2*time.Hour)).Error)
		require.NoError(t, sweepThumbnails(f.ctx, f.database, time.Now()))
		require.NoError(t, sweepThumbnails(f.ctx, f.database, time.Now()))
		row, err := newFileStore(f.database).File(f.ctx, 1, view.ID)
		require.NoError(t, err)
		require.Equal(t, min(attempt, 3), row.ThumbAttempts)
		assertThumbnailOutbox(t, f, view.ID, int64(1+min(attempt, 3)))
	}
	redis := f.identityFixture.redis.cache
	cfg, publisher := thumbnailQueueFixture(t, f)
	handle := thumbnailEventHandler(publisher, func(_ context.Context, _ outbox.Event) error { return nil })
	dispatcher, err := outbox.NewDispatcher(f.database, publisher, nil)
	require.NoError(t, err)
	consumer, err := outbox.NewConsumer(dispatcher, redis, handle)
	require.NoError(t, err)
	require.NoError(t, dispatcher.RunOnce(f.ctx))
	var deliveries []struct {
		ID       int64
		Attempts int
	}
	require.NoError(t, q.Table("outbox_event").Select("id,attempts").Where("event_type='thumb.generate'").Order("id").Find(&deliveries).Error)
	for _, delivery := range deliveries {
		require.NoError(t, consumer.Handle(f.ctx, delivery.ID, delivery.Attempts))
	}
	inspector := asynq.NewInspector(worker.RedisOptions(cfg))
	defer func() { require.NoError(t, inspector.Close()) }()
	tasks, err := inspector.ListPendingTasks("thumb")
	require.NoError(t, err)
	require.Len(t, tasks, 1, "concurrent dispatches deduplicate the same file")
	require.Zero(t, tasks[0].MaxRetry)
	var payload worker.ThumbnailPayload
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &payload))
	require.Equal(t, worker.ThumbnailPayload{TenantID: 1, FileID: view.ID}, payload)
	t.Logf("#5 pending file %d: 3 requeues max; duplicate sweeps do not increment; thumb:generate payload=%s timeout=%s retry=%d", view.ID, tasks[0].Payload, tasks[0].Timeout, tasks[0].MaxRetry)
}

func thumbnailPNGDimensions(t *testing.T, width, height uint32) []byte {
	t.Helper()
	data := fileModelImage(t, "png")
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}

func assertThumbnailOutbox(t *testing.T, f *fileStorageFixture, id, expected int64) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("outbox_event").Where("tenant_id=1 AND aggregate_id=? AND event_type='thumb.generate'", fmt.Sprint(id)).Count(&count).Error)
	require.Equal(t, expected, count)
}

func assertThumbnailReady(t *testing.T, f *fileStorageFixture, id int64) filemodule.View {
	t.Helper()
	view, err := f.files.Metadata(f.ctx, 1, id)
	require.NoError(t, err)
	require.Equal(t, "ready", view.ThumbStatus)
	require.NotNil(t, view.ThumbURL)
	status, data, _ := fileModelGet(f.ctx, t, *view.ThumbURL)
	require.Equal(t, 200, status)
	config, kind, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, "jpeg", kind)
	require.LessOrEqual(t, max(config.Width, config.Height), 320)
	t.Logf("file_id=%d thumb GET=%d JPEG=%dx%d", id, status, config.Width, config.Height)
	return view
}
