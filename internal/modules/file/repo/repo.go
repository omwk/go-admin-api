// Package repo declares file metadata persistence boundaries.
package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

// FileStore persists tenant-scoped file records.
type FileStore interface {
	FileTransaction(context.Context, func(context.Context) error) error
	FindFileByHash(context.Context, int64, string) (model.FileObject, error)
	CreateFile(context.Context, *model.FileObject) error
	File(context.Context, int64, int64) (model.FileObject, error)
}

// FileHashLocker serializes tenant content registration.
type FileHashLocker interface {
	LockFileHash(context.Context, int64, string) error
}
