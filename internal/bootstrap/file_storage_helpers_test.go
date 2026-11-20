package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

type fileStorageFixture struct {
	*adminFixture
	providers *system.StorageService
	files     *filemodule.Service
	resolver  *storage.Resolver
	sealer    crypto.Sealer
	input     system.StorageInput
	store     *storageStore
}

func newFileStorageFixture(t *testing.T) *fileStorageFixture {
	t.Helper()
	a := newAdminFixture(t)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: shopRandomBytes(t, 32)}, 1)
	require.NoError(t, err)
	f := &fileStorageFixture{adminFixture: a, sealer: sealer, store: &storageStore{database: a.database}}
	record := func(ctx context.Context, e audit.Event) error { return RecordAudit(ctx, a.database, e) }
	f.providers, err = system.NewStorageService(f.store, sealer, record, newIdempotencyStore(a.database))
	require.NoError(t, err)
	f.resolver, err = storage.NewResolver(f.store, sealer)
	require.NoError(t, err)
	f.files, err = filemodule.NewResolvedFileService(newFileStore(a.database), fileResolver(f.resolver), newIdempotencyStore(a.database), record)
	require.NoError(t, err)
	profiles, err := identity.NewProfileService(newProfileStore(a.database))
	require.NoError(t, err)
	require.NoError(t, system.RegisterStorageRoutes(a.router, a.identityFixture.service, profiles, f.providers))
	require.NoError(t, filemodule.RegisterFileRoutes(a.router, a.identityFixture.service, profiles, f.files))
	secure, port := false, 9000
	f.input = system.StorageInput{Name: "integration", Endpoint: "127.0.0.1", Port: &port, UseSSL: &secure, Bucket: "storage-test", AccessKey: stringHex(t, 16), SecretKey: stringHex(t, 32), IsDefault: true}
	return f
}

func stringHex(t *testing.T, n int) string {
	t.Helper()
	const digits = "0123456789abcdef"
	raw := shopRandomBytes(t, n)
	encoded := make([]byte, len(raw)*2)
	for i, b := range raw {
		encoded[i*2], encoded[i*2+1] = digits[b>>4], digits[b&15]
	}
	return string(encoded)
}

func (f *fileStorageFixture) useMinIO(t *testing.T) {
	t.Helper()
	_, bucket := exportS3Fixture(f.ctx, t)
	f.input.Endpoint, f.input.Bucket = testenv.Lookup("CH_STORAGE_ENDPOINT"), bucket
	f.input.AccessKey, f.input.SecretKey = testenv.Lookup("CH_STORAGE_ACCESS_KEY"), testenv.Lookup("CH_STORAGE_SECRET_KEY")
	if f.input.Endpoint == "" {
		f.input.Endpoint = "http://localhost:9000"
	}
}

func (f *fileStorageFixture) request(t *testing.T, method, path string, payload any, key string) (*httptest.ResponseRecorder, adminEnvelope) {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	r := httptest.NewRequest(method, "/api/v1/admin"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w, f.envelope(t, method, path, w)
}

func (f *fileStorageFixture) envelope(t *testing.T, method, path string, w *httptest.ResponseRecorder) adminEnvelope {
	t.Helper()
	name := "file"
	if strings.HasPrefix(path, "/storage-providers") {
		name = "storage"
	}
	segments := strings.Split(strings.Split(path, "?")[0], "/")
	if len(segments) > 2 {
		if _, err := strconv.ParseInt(segments[2], 10, 64); err == nil {
			segments[2] = "{id}"
		}
	}
	if method == "GET" && name == "file" && len(segments) == 3 && w.Code == 200 {
		return adminEnvelope{}
	}
	validateOpenAPIResponse(t, readStorageContract(t, name), "/api/v1/admin"+strings.Join(segments, "/"), strings.ToLower(method), w.Code, w.Body.Bytes())
	var value adminEnvelope
	if w.Code != 204 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &value))
	}
	return value
}

func (f *fileStorageFixture) save(t *testing.T, input system.StorageInput, id int64, key string) storage.Provider {
	t.Helper()
	method, path, status := "POST", "/storage-providers", 201
	if id > 0 {
		method, path, status = "PUT", path+"/"+strconv.FormatInt(id, 10), 200
	}
	w, envelope := f.request(t, method, path, input, key)
	require.Equal(t, status, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var p storage.Provider
	require.NoError(t, json.Unmarshal(envelope.Data, &p))
	return p
}

func (f *fileStorageFixture) upload(t *testing.T, name, purpose string, body []byte, key string) (*httptest.ResponseRecorder, filemodule.View) {
	t.Helper()
	var buffer bytes.Buffer
	mw := multipart.NewWriter(&buffer)
	part, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(body)
	require.NoError(t, err)
	// Purpose deliberately follows the file to guard multipart field ordering.
	require.NoError(t, mw.WriteField("purpose", purpose))
	require.NoError(t, mw.Close())
	r := httptest.NewRequest("POST", "/api/v1/admin/files", &buffer)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	envelope := f.envelope(t, "POST", "/files", w)
	var view filemodule.View
	if w.Code == 201 {
		require.NoError(t, json.Unmarshal(envelope.Data, &view))
	}
	return w, view
}

func fileStorageDownload(ctx context.Context, t *testing.T, signed string, expected []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, expected, body)
}
