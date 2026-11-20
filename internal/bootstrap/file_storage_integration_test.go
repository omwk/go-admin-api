package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	identitymodel "github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationProviderCRUDEncryptionAndAudit(t *testing.T) {
	f := newFileStorageFixture(t)
	first := f.save(t, f.input, 0, "create")
	replayed := f.save(t, f.input, 0, "create")
	require.Equal(t, first.ID, replayed.ID)
	require.True(t, first.SecretKey == f.input.SecretKey, "authorized response must return submitted plaintext")
	w, detail := f.request(t, "GET", fmt.Sprintf("/storage-providers/%d", first.ID), nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var got storage.Provider
	require.NoError(t, json.Unmarshal(detail.Data, &got))
	require.True(t, got.SecretKey == f.input.SecretKey)
	stored, err := f.store.Provider(f.ctx, first.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.SecretEnc)
	require.False(t, bytes.Contains(stored.SecretEnc, []byte(f.input.SecretKey)))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var cipher string
	require.NoError(t, q.Raw("SELECT encode(secret_enc,'hex') FROM storage_provider WHERE id=?", first.ID).Scan(&cipher).Error)
	t.Logf("acceptance 2+3: GET secret_key equals submitted plaintext=true; SELECT encode(secret_enc,'hex')=%s", cipher)
	w, list := f.request(t, "GET", "/storage-providers", nil, "")
	require.Equal(t, 200, w.Code)
	var rows []storage.Provider
	require.NoError(t, json.Unmarshal(list.Data, &rows))
	require.Len(t, rows, 1)
	require.True(t, rows[0].SecretKey == f.input.SecretKey)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	f.input.Name = "second"
	second := f.save(t, f.input, 0, "second")
	old, err := f.store.Provider(f.ctx, first.ID)
	require.NoError(t, err)
	require.False(t, old.IsDefault)
	w, _ = f.request(t, "DELETE", fmt.Sprintf("/storage-providers/%d", second.ID), nil, "")
	require.Equal(t, 409, w.Code)
	w, _ = f.request(t, "POST", fmt.Sprintf("/storage-providers/%d/default", first.ID), nil, "default")
	require.Equal(t, 200, w.Code)
	w, _ = f.request(t, "DELETE", fmt.Sprintf("/storage-providers/%d", second.ID), nil, "delete")
	require.Equal(t, 204, w.Code)
	w, _ = f.request(t, "DELETE", fmt.Sprintf("/storage-providers/%d", second.ID), nil, "delete")
	require.Equal(t, 204, w.Code)
	f.input.Name = "updated"
	f.save(t, f.input, first.ID, "update")
	f.assertStorageAudits(t, f.input.SecretKey)
	var responses []string
	require.NoError(t, q.Table("idempotency_key").Pluck("response_body::text", &responses).Error)
	require.NotContains(t, fmt.Sprint(responses), f.input.SecretKey)
	t.Log("acceptance 1,4,5,6,14: create/list, encrypted idempotency replay, unique default, switch/delete guard, sanitized audits verified")
}

func (f *fileStorageFixture) assertStorageAudits(t *testing.T, secret string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var records []struct {
		Action        string
		After, Before json.RawMessage
	}
	require.NoError(t, q.Table("audit_log").Select("action,\"after\",\"before\"").Where("action LIKE 'storage.provider.%'").Find(&records).Error)
	actions := map[string]int{}
	for _, row := range records {
		actions[row.Action]++
		require.False(t, bytes.Contains(append(row.After, row.Before...), []byte(secret)))
	}
	require.Equal(t, 2, actions["storage.provider.create"])
	require.Equal(t, 1, actions["storage.provider.delete"])
	require.Equal(t, 2, actions["storage.provider.update"])
}

func TestFileStorageIntegrationUnconfiguredAndPermissions(t *testing.T) {
	f := newFileStorageFixture(t)
	w, _ := f.upload(t, "test.csv", "attachment", []byte("a,b\n1,2\n"), "")
	require.Equal(t, 409, w.Code)
	require.Contains(t, w.Body.String(), "对象存储未配置")
	require.Contains(t, f.actor.Permissions, permission.SystemStorageManage)
	require.Contains(t, f.actor.Permissions, permission.SystemFileManage)
	f.bearer = f.identityFixture.login(t).AccessToken
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		path := "/storage-providers/1"
		if method == "POST" {
			path = "/storage-providers"
		}
		w, _ = f.request(t, method, path, nil, "")
		require.Equal(t, 404, w.Code)
	}
	for _, suffix := range []string{"test", "default"} {
		w, _ = f.request(t, "POST", "/storage-providers/1/"+suffix, nil, "")
		require.Equal(t, 404, w.Code)
	}
	w, _ = f.request(t, "GET", "/files", nil, "")
	require.Equal(t, 403, w.Code)
	t.Log("acceptance 12,13: no-provider upload=409; non-admin provider routes=404; file permission enforced; seed grants both manage permissions")
}

func TestFileStorageIntegrationMinIOLiveConfigurationAndHistoricalDownload(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	first := f.save(t, f.input, 0, "")
	f.connectionResults(t, first.ID)
	body := []byte("item,quantity\nbook,2\n")
	w, file := f.upload(t, "文件.csv", "shop", body, "upload")
	require.Equal(t, 201, w.Code)
	require.Equal(t, "shop", file.Purpose)
	require.Equal(t, "text/csv", file.MIME)
	row, err := newFileStore(f.database).File(f.ctx, 1, file.ID)
	require.NoError(t, err)
	require.NotNil(t, row.ProviderID)
	require.Equal(t, first.ID, *row.ProviderID)
	require.Regexp(t, `^tenants/1/shop/\d{4}/\d{2}/[a-f0-9]+-文件.csv$`, row.ObjectKey)
	f.input.Name, f.input.Bucket = "other-default", "unavailable-other-bucket"
	f.save(t, f.input, 0, "")
	f.checkDownloads(t, file.ID, body)
	w, _ = f.request(t, "GET", "/files?purpose=shop&keyword=文件&page=1&page_size=10", nil, "")
	require.Equal(t, 200, w.Code)
	f.checkFileIsolation(t, file.ID)
	w, _ = f.request(t, "DELETE", "/files/"+strconv.FormatInt(file.ID, 10), nil, "delete-file")
	require.Equal(t, 204, w.Code)
	w, _ = f.request(t, "DELETE", "/files/"+strconv.FormatInt(file.ID, 10), nil, "delete-file")
	require.Equal(t, 204, w.Code)
	t.Log("acceptance 8,10,11: saved provider -> same running router upload=201; switched default -> original stream and five-minute presign downloaded exact bytes; cross-tenant read/delete=404")
}

func (f *fileStorageFixture) connectionResults(t *testing.T, id int64) {
	t.Helper()
	path := fmt.Sprintf("/storage-providers/%d/test", id)
	for _, expected := range []string{"ok", "access_denied", "bucket_missing"} {
		original := f.input
		if expected == "access_denied" {
			f.input.SecretKey = stringHex(t, 32)
		}
		if expected == "bucket_missing" {
			f.input.Bucket = "missing-" + stringHex(t, 8)
		}
		if expected != "ok" {
			f.save(t, f.input, id, "")
		}
		w, response := f.request(t, "POST", path, nil, "")
		require.Equal(t, 200, w.Code)
		var result map[string]string
		require.NoError(t, json.Unmarshal(response.Data, &result))
		require.Equal(t, expected, result["status"])
		f.input = original
		if expected != "ok" {
			f.save(t, f.input, id, "")
		}
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var auditRows []json.RawMessage
	require.NoError(t, q.Table("audit_log").Where("action='storage.provider.test'").Pluck("\"after\"", &auditRows).Error)
	require.Len(t, auditRows, 3)
	for _, row := range auditRows {
		require.False(t, bytes.Contains(row, []byte(f.input.SecretKey)))
		require.Contains(t, string(row), "***")
	}
	t.Log("acceptance 7: real MinIO connection statuses ok/access_denied/bucket_missing verified")
}

func (f *fileStorageFixture) checkDownloads(t *testing.T, id int64, body []byte) {
	t.Helper()
	path := fmt.Sprintf("/files/%d", id)
	w, _ := f.request(t, "GET", path, nil, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, body, w.Body.Bytes())
	w, response := f.request(t, "GET", path+"/url", nil, "")
	require.Equal(t, 200, w.Code)
	var signed struct {
		URL       string
		ExpiresIn int `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal(response.Data, &signed))
	require.Equal(t, 300, signed.ExpiresIn)
	fileStorageDownload(f.ctx, t, signed.URL, body)
}

func (f *fileStorageFixture) checkFileIsolation(t *testing.T, id int64) {
	t.Helper()
	_, _, _, _, err := f.files.Get(f.ctx, 2, id)
	require.Error(t, err)
	require.ErrorIs(t, err, filemodule.ErrFileNotFound)
	require.ErrorIs(t, f.files.Delete(f.ctx, 2, f.actor.User.ID, id), filemodule.ErrFileNotFound)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Exec("INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,?,?)", "file-foreign", "foreign files").Error)
	dept := identitymodel.Department{TenantID: 2, Name: "file-foreign", Path: "foreign", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := identitymodel.User{TenantID: 2, Username: "file-foreign", PasswordHash: f.user.PasswordHash, DepartmentID: dept.ID, Status: "active", IsSuper: true}
	require.NoError(t, q.Create(&user).Error)
	tokens, err := f.identityFixture.service.Login(f.ctx, 2, user.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	previous := f.bearer
	f.bearer = tokens.AccessToken
	defer func() { f.bearer = previous }()
	for _, method := range []string{"GET", "DELETE"} {
		w, _ := f.request(t, method, fmt.Sprintf("/files/%d", id), nil, "")
		require.Equal(t, 404, w.Code)
	}
	w, _ := f.request(t, "GET", "/storage-providers", nil, "")
	require.Equal(t, 404, w.Code)
}
