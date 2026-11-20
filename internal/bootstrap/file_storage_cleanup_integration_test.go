package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationMinIODeleteRetriesAfterCommit(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	first := f.save(t, f.input, 0, "")
	w, file := f.upload(t, "cleanup.csv", "attachment", []byte("a,b\n1,2\n"), "")
	require.Equal(t, 201, w.Code)
	row, err := newFileStore(f.database).File(f.ctx, 1, file.ID)
	require.NoError(t, err)
	f.input.Name, f.input.Bucket = "new-default", "unused-"+stringHex(t, 8)
	f.save(t, f.input, 0, "")
	path := "/files/" + formatStorageID(file.ID)
	w, _ = f.request(t, "DELETE", path, nil, "durable-delete")
	require.Equal(t, 204, w.Code)
	w, _ = f.request(t, "DELETE", path, nil, "durable-delete")
	require.Equal(t, 204, w.Code)
	w, _ = f.request(t, "GET", path, nil, "")
	require.Equal(t, 404, w.Code)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var events []outbox.Event
	require.NoError(t, q.Table("outbox_event").Where("event_type='file.cleanup'").Find(&events).Error)
	require.Len(t, events, 1)
	var audits int64
	require.NoError(t, q.Table("audit_log").Where("action='file.delete'").Count(&audits).Error)
	require.EqualValues(t, 1, audits)
	w, _ = f.request(t, "DELETE", "/storage-providers/"+formatStorageID(first.ID), nil, "")
	require.Equal(t, 409, w.Code)
	handle := fileCleanupHandler(f.resolver)
	require.Error(t, f.database.Transaction(f.ctx, func(tx context.Context) error { return handle(tx, events[0]) }))
	foreign := events[0]
	foreign.TenantID = 2
	require.Error(t, handle(f.ctx, foreign))
	firstInput := f.input
	firstInput.Bucket, firstInput.IsDefault = row.Bucket, false
	goodSecret := firstInput.SecretKey
	firstInput.SecretKey = stringHex(t, 32)
	f.save(t, firstInput, first.ID, "")
	require.Error(t, handle(f.ctx, events[0]))
	firstInput.SecretKey, firstInput.Bucket = goodSecret, "edited-"+stringHex(t, 8)
	f.save(t, firstInput, first.ID, "")
	require.NoError(t, handle(f.ctx, events[0]))
	require.NoError(t, handle(f.ctx, events[0]))
	selected, err := f.resolver.Resolve(f.ctx, &first.ID)
	require.NoError(t, err)
	_, err = selected.Object.WithBucket(row.Bucket).Get(f.ctx, row.ObjectKey)
	require.Error(t, err)
	t.Log("file deletion: committed metadata and single audit/outbox; no object IO in transaction; failed cleanup retried with historical provider and bucket")
}
