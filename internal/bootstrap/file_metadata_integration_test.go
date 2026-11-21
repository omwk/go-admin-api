package bootstrap

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	filemodel "github.com/omwk/go-admin-api/internal/modules/file/model"
	identitymodel "github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestFileMetadataIntegrationUploaderAndPrivacy(t *testing.T) {
	// BE-IDN-024 §3.5/6: live names, soft deletion and absent users share a safe projection.
	f := newFileStorageFixture(t)
	store := newFileStore(f.database)
	row := filemodel.FileObject{TenantID: 1, Bucket: "metadata", ObjectKey: "metadata.csv", OriginalName: "metadata.csv", MIME: "text/csv", SizeBytes: 4, SHA256: stringHex(t, 32), Purpose: "attachment", UploadedBy: &f.user.ID}
	require.NoError(t, store.CreateFile(f.ctx, &row))
	assertFileMetadata(t, f, row.ID, &f.user.ID, f.user.Username)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Where("tenant_id=? AND id=?", 1, f.user.ID).Delete(&identitymodel.User{}).Error)
	assertFileMetadata(t, f, row.ID, &f.user.ID, "#"+strconv.FormatInt(f.user.ID, 10))
	missing := int64(99999999)
	require.NoError(t, q.Model(&filemodel.FileObject{}).Where("id=?", row.ID).Update("uploaded_by", missing).Error)
	assertFileMetadata(t, f, row.ID, &missing, "#99999999")
	require.NoError(t, q.Model(&filemodel.FileObject{}).Where("id=?", row.ID).Update("uploaded_by", nil).Error)
	assertFileMetadata(t, f, row.ID, nil, "")
	t.Log("BE-IDN-024 list+metadata: username; deleted/missing -> #id; null -> null/empty; only safe fields")
}

func assertFileMetadata(t *testing.T, f *fileStorageFixture, id int64, user *int64, name string) {
	t.Helper()
	w, envelope := f.request(t, "GET", "/files", nil, "")
	require.Equal(t, 200, w.Code)
	var page struct{ List []json.RawMessage }
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Len(t, page.List, 1)
	metadata := "/files/" + strconv.FormatInt(id, 10) + "/metadata"
	w, detail := f.request(t, "GET", metadata, nil, "")
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, string(page.List[0]), string(detail.Data))
	var fields map[string]any
	require.NoError(t, json.Unmarshal(detail.Data, &fields))
	require.Len(t, fields, 11)
	require.Equal(t, "sheet", fields["category"])
	require.Equal(t, "attachment", fields["source"])
	require.Nil(t, fields["thumb_url"])
	require.NotContains(t, fields, "purpose")
	require.Contains(t, fields, "uploaded_by")
	if user == nil {
		require.Nil(t, fields["uploaded_by"])
	} else {
		require.Equal(t, float64(*user), fields["uploaded_by"])
	}
	require.Equal(t, name, fields["uploaded_by_name"])
	for _, key := range []string{"email", "phone", "password_hash", "tenant_id", "object_key", "bucket"} {
		require.NotContains(t, fields, key)
	}
	t.Logf("GET %s: %s", metadata, detail.Data)
}

func TestFileMetadataIntegrationTenantIsolation(t *testing.T) {
	f := newFileStorageFixture(t)
	store := newFileStore(f.database)
	row := filemodel.FileObject{TenantID: 2, Bucket: "metadata", ObjectKey: "foreign.csv", OriginalName: "foreign.csv", MIME: "text/csv", SizeBytes: 4, SHA256: stringHex(t, 32), Purpose: "attachment", UploadedBy: &f.actor.User.ID}
	require.NoError(t, store.CreateFile(f.ctx, &row))
	w, _ := f.request(t, "GET", fmt.Sprintf("/files/%d/metadata", row.ID), nil, "")
	require.Equal(t, 404, w.Code)
	w, envelope := f.request(t, "GET", "/files", nil, "")
	require.Equal(t, 200, w.Code)
	var page struct{ Total int64 }
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Zero(t, page.Total)
	foreign, err := store.File(f.ctx, 2, row.ID)
	require.NoError(t, err)
	require.Empty(t, foreign.UploadedByName, "uploader joins must include tenant identity")
	metadata, err := f.files.Metadata(f.ctx, 2, row.ID)
	require.NoError(t, err)
	require.Equal(t, "#"+strconv.FormatInt(f.actor.User.ID, 10), metadata.UploadedByName)
	for _, invalid := range []struct {
		path   string
		status int
	}{{"/files/0/metadata", 400}, {"/files/no-id/metadata", 400}, {"/files/99999999/metadata", 404}} {
		w, _ = f.request(t, "GET", invalid.path, nil, "")
		require.Equal(t, invalid.status, w.Code)
	}
	f.bearer = f.login(t).AccessToken
	w, _ = f.request(t, "GET", fmt.Sprintf("/files/%d/metadata", row.ID), nil, "")
	require.Equal(t, 403, w.Code)
}
