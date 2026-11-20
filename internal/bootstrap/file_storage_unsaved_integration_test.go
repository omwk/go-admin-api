package bootstrap

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationUnsavedTest(t *testing.T) {
	// BE-IDN-024 §3.7: test the submitted configuration against real MinIO before saving.
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	body := map[string]any{"endpoint": f.input.Endpoint, "port": *f.input.Port, "bucket": f.input.Bucket, "use_ssl": false, "region": "us-east-1", "access_key": f.input.AccessKey, "secret_key": f.input.SecretKey}
	for _, status := range []string{"ok", "access_denied", "bucket_missing"} {
		t.Run(status, func(t *testing.T) {
			body["secret_key"], body["bucket"] = f.input.SecretKey, f.input.Bucket
			if status == "access_denied" {
				body["secret_key"] = stringHex(t, 32)
			}
			if status == "bucket_missing" {
				body["bucket"] = "missing-" + stringHex(t, 8)
			}
			w, envelope := f.request(t, "POST", "/storage-providers/test", body, "unsaved-"+status)
			require.Equal(t, 200, w.Code)
			require.JSONEq(t, `{"status":"`+status+`"}`, string(envelope.Data))
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.False(t, bytes.Contains(w.Body.Bytes(), []byte(body["secret_key"].(string))))
			require.False(t, bytes.Contains(w.Body.Bytes(), []byte(f.input.AccessKey)))
			replay, _ := f.request(t, "POST", "/storage-providers/test", body, "unsaved-"+status)
			require.Equal(t, 200, replay.Code)
			rows, err := f.store.Providers(f.ctx)
			require.NoError(t, err)
			require.Empty(t, rows, "testing a connection must not persist a provider")
			redacted := map[string]any{}
			for key, value := range body {
				redacted[key] = value
			}
			redacted["access_key"], redacted["secret_key"] = "<redacted>", "<redacted>"
			raw, err := json.Marshal(redacted)
			require.NoError(t, err)
			t.Logf("POST /api/v1/admin/storage-providers/test request=%s response=%s providers=0", raw, w.Body.String())
		})
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var audits []json.RawMessage
	require.NoError(t, q.Table("audit_log").Where("action='storage.provider.test'").Pluck("\"after\"", &audits).Error)
	require.Len(t, audits, 3, "idempotent replays must not duplicate probe audits")
	for _, raw := range audits {
		require.False(t, bytes.Contains(raw, []byte(f.input.SecretKey)))
		require.False(t, bytes.Contains(raw, []byte(f.input.AccessKey)))
		require.Contains(t, string(raw), "***")
	}
}

func TestFileStorageIntegrationUnsavedValidationAndCompatibility(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	saved := f.save(t, f.input, 0, "")
	for _, body := range []any{
		nil,
		map[string]any{"endpoint": f.input.Endpoint, "bucket": f.input.Bucket, "access_key": f.input.AccessKey},
		map[string]any{"endpoint": "ftp://invalid", "bucket": f.input.Bucket, "access_key": f.input.AccessKey, "secret_key": stringHex(t, 32)},
		map[string]any{"unknown": true},
	} {
		w, _ := f.request(t, "POST", "/storage-providers/test", body, "")
		require.Equal(t, 400, w.Code)
	}
	// The existing saved-provider test remains available and defaults remain untouched.
	w, _ := f.request(t, "POST", resourcePath("storage-providers", saved.ID)+"/test", nil, "")
	require.Equal(t, 200, w.Code)
	current, err := f.store.Provider(f.ctx, saved.ID)
	require.NoError(t, err)
	require.True(t, current.IsDefault)
	require.Equal(t, saved.ID, current.ID)
	f.bearer = f.login(t).AccessToken
	w, _ = f.request(t, "POST", "/storage-providers/test", f.input, "")
	require.Equal(t, 404, w.Code)
	f.bearer = ""
	w, _ = f.request(t, "POST", "/storage-providers/test", f.input, "")
	require.Equal(t, 401, w.Code)
}
