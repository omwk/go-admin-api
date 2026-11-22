package file

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/stretchr/testify/require"
)

type thumbnailMemoryStore struct {
	*memoryFileStore
	beforeComplete func()
}

func (s *thumbnailMemoryStore) CompleteThumbnail(_ context.Context, tenant, id int64, key, status string) error {
	if s.beforeComplete != nil {
		s.beforeComplete()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.TenantID != tenant {
		return ErrFileNotFound
	}
	if row.ThumbStatus == "pending" {
		row.ThumbKey, row.ThumbStatus = key, status
		s.rows[id] = row
	}
	return nil
}

func TestFileThumbnailWorkerTerminalStates(t *testing.T) {
	// BE-IDN-028 §4.4/6: permanent decoder errors terminate; originals remain intact.
	for _, test := range []struct {
		name, status string
		data         []byte
	}{
		{"valid", "ready", fileImage(t, "png", 2048, 2048)},
		{"corrupt", "failed", append(jpegHeader(), bytes.Repeat([]byte("x"), imageInputLimit)...)},
		{"pixels", "failed", thumbnailPNGHeader(t, 10000, 4001)},
		{"bytes", "failed", bytes.Repeat([]byte("x"), int(MaxFileSize)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, store, objects := newThumbnailMemory(t, test.data)
			require.NoError(t, worker.Generate(context.Background(), 1, 1))
			require.Equal(t, test.status, store.rows[1].ThumbStatus)
			require.Equal(t, test.data, objects.objects["tenants/1/photo.png"])
			require.NoError(t, worker.Generate(context.Background(), 1, 1))
			require.Zero(t, store.rows[1].ThumbAttempts)
		})
	}
}

func TestFileThumbnailWorkerInfrastructureAndCancellationRemainPending(t *testing.T) {
	w, store, objects := newThumbnailMemory(t, fileImage(t, "png", 640, 320))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, w.Generate(ctx, 1, 1), context.Canceled)
	require.Equal(t, "pending", store.rows[1].ThumbStatus)
	delete(objects.objects, "tenants/1/photo.png")
	require.Error(t, w.Generate(context.Background(), 1, 1))
	require.Equal(t, "pending", store.rows[1].ThumbStatus)
	require.NoError(t, w.Generate(context.Background(), 2, 1))
	require.Equal(t, "pending", store.rows[1].ThumbStatus)
}

func TestFileThumbnailWorkerCrashRecoveryAndConcurrentDelete(t *testing.T) {
	data := fileImage(t, "png", 640, 320)
	w, store, objects := newThumbnailMemory(t, data)
	key := "tenants/1/photo.png.thumb.jpg"
	objects.objects[key] = []byte("previous successful write")
	require.NoError(t, w.Generate(context.Background(), 1, 1))
	require.Equal(t, "ready", store.rows[1].ThumbStatus)
	w, store, objects = newThumbnailMemory(t, data)
	store.beforeComplete = func() { delete(store.rows, 1) }
	require.NoError(t, w.Generate(context.Background(), 1, 1))
	require.NotContains(t, objects.objects, key)
}

func TestFileThumbnailProjectionHidesNonReadyKeys(t *testing.T) {
	service, _, _ := newFileTestService(t)
	for _, status := range []string{"", "pending", "failed", "ready"} {
		view, err := service.project(context.Background(), model.FileObject{MIME: "image/jpeg", ThumbKey: "preview", ThumbStatus: status})
		require.NoError(t, err)
		require.Equal(t, status == "ready", view.ThumbURL != nil)
	}
}

func newThumbnailMemory(t *testing.T, data []byte) (*ThumbnailWorker, *thumbnailMemoryStore, *memoryObjectStore) {
	t.Helper()
	_, objects, memory := newFileTestService(t)
	store := &thumbnailMemoryStore{memoryFileStore: memory}
	store.rows[1] = model.FileObject{ID: 1, TenantID: 1, Bucket: "original", ObjectKey: "tenants/1/photo.png", MIME: "image/png", ThumbStatus: "pending"}
	objects.objects["tenants/1/photo.png"] = data
	w, err := NewThumbnailWorker(store, func(_ context.Context, id *int64, bucket string) (Binding, error) {
		if bucket != "original" || id != nil {
			return Binding{}, errors.New("historical storage identity changed")
		}
		return Binding{Object: objects, Bucket: bucket}, nil
	})
	require.NoError(t, err)
	return w, store, objects
}

func thumbnailPNGHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	data := fileImage(t, "png", 1, 1)
	binary.BigEndian.PutUint32(data[16:20], width)
	binary.BigEndian.PutUint32(data[20:24], height)
	binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
	return data
}
