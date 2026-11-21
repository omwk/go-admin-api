package file

import (
	"context"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/stretchr/testify/require"
)

func (s *memoryFileStore) FileUsage(_ context.Context, tenant, user int64) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var used, count int64
	for _, row := range s.rows {
		if row.TenantID == tenant && row.UploadedBy != nil && *row.UploadedBy == user {
			used += row.SizeBytes
			count++
		}
	}
	return used, count, nil
}
func (s *memoryFileStore) LockFileUser(context.Context, int64, int64) error { return nil }

func TestFileQuotaCheckedBeforeWritingObjects(t *testing.T) {
	service, objects, store := newFileTestService(t)
	service.quotaPerUserMB = 1
	owner := int64(2)
	store.rows[1] = model.FileObject{TenantID: 1, UploadedBy: &owner, SizeBytes: 1 << 20}
	_, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: "notes.txt", SizeBytes: 4, Reader: strings.NewReader("text")})
	require.Error(t, err)
	require.Equal(t, 413, errs.From(err).HTTPStatus)
	require.Equal(t, "存储空间不足：已用 1 MB / 上限 1 MB", errs.From(err).Message)
	require.Empty(t, objects.keys())
	usage, err := service.Usage(context.Background(), 1, owner)
	require.NoError(t, err)
	require.Equal(t, Usage{UsedBytes: 1 << 20, QuotaBytes: 1 << 20, FileCount: 1}, usage)
	// Another user's or tenant's files never consume this user's allowance.
	_, _, err = service.Upload(context.Background(), Input{TenantID: 1, UserID: 3, OriginalName: "notes.txt", SizeBytes: 4, Reader: strings.NewReader("text")})
	require.NoError(t, err)
}

func TestFileQuotaWithIdempotencyCheckedBeforeWritingObjects(t *testing.T) {
	service, objects, store := newFileTestService(t)
	service.quotaPerUserMB = 1
	owner := int64(2)
	store.rows[1] = model.FileObject{TenantID: 1, UploadedBy: &owner, SizeBytes: 1 << 20}
	_, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: owner, OriginalName: "notes.txt", SizeBytes: 4, Reader: strings.NewReader("text"), IdempotencyKey: "quota-new"})
	require.Error(t, err)
	require.Equal(t, 413, errs.From(err).HTTPStatus)
	require.Empty(t, objects.keys())
}
