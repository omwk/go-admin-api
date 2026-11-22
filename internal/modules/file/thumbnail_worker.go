package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/file/repo"
)

const thumbnailWorkerPixels = 40_000_000

// ThumbnailStore writes terminal preview states only while metadata is pending.
type ThumbnailStore interface {
	repo.FileStore
	CompleteThumbnail(context.Context, int64, int64, string, string) error
}

// ThumbnailWorker generates bounded previews using each file's pinned storage.
type ThumbnailWorker struct{ service *Service }

// NewThumbnailWorker requires metadata persistence and historical storage resolution.
func NewThumbnailWorker(store ThumbnailStore, resolver Resolver) (*ThumbnailWorker, error) {
	if store == nil || resolver == nil {
		return nil, errors.New("thumbnail worker dependencies required")
	}
	return &ThumbnailWorker{service: &Service{store: store, resolver: resolver}}, nil
}

// Generate leaves infrastructure failures pending and terminates malformed images.
func (w *ThumbnailWorker) Generate(ctx context.Context, tenant, id int64) error {
	if tenant <= 0 || id <= 0 {
		return invalidFile()
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	s := w.service
	row, err := s.store.File(ctx, tenant, id)
	if errors.Is(err, ErrFileNotFound) {
		return nil
	}
	if err != nil || row.ThumbStatus != "pending" || row.Purpose == "avatar" {
		return err
	}
	bound, err := s.bound(ctx, row.ProviderID, row.Bucket)
	if err != nil {
		return err
	}
	data, oversized, err := readThumbnail(ctx, bound.object, row.ObjectKey)
	if err != nil {
		return err
	}
	var thumbnail []byte
	if !oversized {
		// Decode in this slot: cancellation must not leave an unbounded decoder goroutine.
		thumbnail, err = decodeImageLimit(ctx, data, 320, false, thumbnailWorkerPixels)
	}
	if err != nil || oversized {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return s.store.(ThumbnailStore).CompleteThumbnail(ctx, tenant, id, "", "failed")
	}
	key := row.ObjectKey + ".thumb.jpg"
	// An existing deterministic key can be left by a crash before the metadata update.
	if _, err := bound.object.Put(ctx, key, "image/jpeg", bytes.NewReader(thumbnail), int64(len(thumbnail))); err != nil {
		return err
	}
	err = s.store.(ThumbnailStore).CompleteThumbnail(ctx, tenant, id, key, "ready")
	if errors.Is(err, ErrFileNotFound) {
		// A concurrent delete may have dispatched cleanup before this object was written.
		return bound.cleanupObjects(ctx, []string{key})
	}
	return err
}

func readThumbnail(ctx context.Context, object ObjectStore, key string) (data []byte, oversized bool, retErr error) {
	body, _, size, err := object.Open(ctx, key)
	if err != nil {
		return nil, false, err
	}
	defer func() { retErr = errors.Join(retErr, body.Close()) }()
	if size > MaxFileSize {
		return nil, true, nil
	}
	data, err = io.ReadAll(io.LimitReader(&imageReader{ctx: ctx, Reader: body}, MaxFileSize+1))
	return data, int64(len(data)) > MaxFileSize, err
}
