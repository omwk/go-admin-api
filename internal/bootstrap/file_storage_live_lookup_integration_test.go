package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

var storageLookupConfig = flag.String("storage-lookup-config", "", "opt-in live storage acceptance: dotenv file with database DSN and crypto master key")

func TestFileStorageIntegrationLivePathStyleLookup(t *testing.T) {
	// BE-IDN-022 #1/#2: opt-in against the encrypted default provider in the development database.
	if *storageLookupConfig == "" {
		t.Skip("live acceptance requires -storage-lookup-config; uses a persisted provider, never storage environment variables")
	}
	p := liveLookupProvider(t, *storageLookupConfig)
	f := newFileStorageFixture(t)
	f.input.Name, f.input.Endpoint, f.input.Port = "BE-IDN-022 acceptance", p.Endpoint, &p.Port
	f.input.Bucket, f.input.UseSSL, f.input.Region = p.Bucket, &p.UseSSL, p.Region
	f.input.AccessKey, f.input.SecretKey = p.AccessKey, p.SecretKey
	f.input.AccessMode = "private"
	provider := f.save(t, f.input, 0, "")
	require.True(t, provider.PathStyle)
	selected, err := f.resolver.Default(f.ctx)
	require.NoError(t, err)
	bind, err := fileResolver(f.resolver)(f.ctx, &provider.ID, provider.Bucket)
	require.NoError(t, err)
	exports := &resolvedExportObjects{database: f.database, resolver: f.resolver}
	object, _, _, err := exports.BindExport(f.ctx)
	require.NoError(t, err)
	require.Same(t, selected.Object, object)
	assertLiveFileBinding(t, bind, selected.Object)
	body := []byte("BE-IDN-022,path-style\nupload,download,presign\n")
	w, view := f.upload(t, "storage-lookup.csv", "shop", body, "")
	require.Equal(t, http.StatusCreated, w.Code)
	cleanupLiveLookup(t, f, view.ID)
	path := "/files/" + strconv.FormatInt(view.ID, 10)
	w, _ = f.request(t, http.MethodGet, path, nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, body, w.Body.Bytes())
	digest := sha256.Sum256(body)
	streamDigest := sha256.Sum256(w.Body.Bytes())
	t.Logf("BE-IDN-022 #1: endpoint=%s bucket=%s upload=201 bytes=%d GET /files/:id=%d bytes=%d equal=%t sha256=%x stream_sha256=%x", p.Endpoint, p.Bucket, len(body), w.Code, w.Body.Len(), bytes.Equal(body, w.Body.Bytes()), digest, streamDigest)
	w, envelope := f.request(t, http.MethodGet, path+"/url", nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	var signed struct{ URL string }
	require.NoError(t, json.Unmarshal(envelope.Data, &signed))
	u, err := url.Parse(signed.URL)
	require.NoError(t, err)
	require.True(t, len(u.Query().Get("X-Amz-Signature")) > 0)
	request, err := http.NewRequestWithContext(f.ctx, http.MethodGet, signed.URL, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.True(t, err == nil, "presigned GET failed; URL omitted to protect credentials")
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, http.StatusOK, response.StatusCode)
	presigned, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, body, presigned)
	t.Logf("BE-IDN-022 #2/#3: presigned GET=%d bytes=%d equal=%t sha256=%x; shop file and export share Resolver/client=true", response.StatusCode, len(presigned), bytes.Equal(body, presigned), sha256.Sum256(presigned))
}
