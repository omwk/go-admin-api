package bootstrap

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/omwk/go-admin-api/internal/worker"
	"github.com/stretchr/testify/require"
)

func newThumbnailFixture(t *testing.T) *fileStorageFixture {
	t.Helper()
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.input.AccessMode = "public"
	f.save(t, f.input, 0, "")
	fileModelPolicy(t, f, true)
	return f
}

func thumbnailPixelImage(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{color.Black, color.White})
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, img))
	return buffer.Bytes()
}

func thumbnailQueueFixture(t *testing.T, f *fileStorageFixture) (config.Redis, *worker.QueuePublisher) {
	t.Helper()
	cfg := config.Redis{Addr: f.server.Addr(), PoolSize: 10}
	if address := testenv.Lookup("THUMB_REDIS_ADDR"); address != "" {
		cfg.Addr = address
	}
	publisher := worker.NewQueuePublisher(cfg)
	t.Cleanup(func() { require.NoError(t, publisher.Close()) })
	return cfg, publisher
}

func dispatchThumbnailEvents(t *testing.T, f *fileStorageFixture, publisher *worker.QueuePublisher) {
	t.Helper()
	dispatcher, err := outbox.NewDispatcher(f.database, publisher, nil)
	require.NoError(t, err)
	handler := thumbnailEventHandler(publisher, func(context.Context, outbox.Event) error { return nil })
	consumer, err := outbox.NewConsumer(dispatcher, f.identityFixture.redis.cache, handler)
	require.NoError(t, err)
	require.NoError(t, dispatcher.RunOnce(f.ctx))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []struct {
		ID       int64
		Attempts int
	}
	require.NoError(t, q.Table("outbox_event").Select("id,attempts").Where("event_type='thumb.generate' AND status='processing'").Find(&events).Error)
	for _, event := range events {
		require.NoError(t, consumer.Handle(f.ctx, event.ID, event.Attempts))
	}
}

func startThumbnailRuntime(t *testing.T, f *fileStorageFixture, cfg config.Redis, wrap func(func(context.Context, int64, int64) error) func(context.Context, int64, int64) error) {
	t.Helper()
	thumbnails, err := filemodule.NewThumbnailWorker(newFileStore(f.database), fileResolver(f.resolver))
	require.NoError(t, err)
	generate := thumbnails.Generate
	if wrap != nil {
		generate = wrap(generate)
	}
	log, err := logger.New(io.Discard, "info", "json")
	require.NoError(t, err)
	server, err := worker.NewThumbnailQueueServer(cfg, log, generate)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(30 * time.Second):
			t.Error("thumbnail runtime did not drain")
		}
	})
}
