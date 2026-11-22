package file

import (
	"bytes"
	"context"
	"image"

	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

func (s *Service) prepareThumbnail(ctx context.Context, row *model.FileObject, data []byte) {
	if row.Category != "image" {
		return
	}
	row.ThumbStatus = "pending"
	if row.SizeBytes > imageInputLimit {
		return
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		row.ThumbStatus = "failed"
		return
	}
	if int64(config.Width) > imagePixelLimit/int64(config.Height) {
		return
	}
	key, err := s.thumbnail(ctx, row.ObjectKey, data)
	if err != nil {
		// Keep the original; storage/deadline failures can be recovered by the worker.
		return
	}
	row.ThumbKey, row.ThumbStatus = key, "ready"
}
