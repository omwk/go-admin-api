package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func TestS3PathStyleUploadDownloadPresign(t *testing.T) {
	// BE-IDN-022: exercise the real SDK against a path-only HTTP endpoint.
	server := pathStorageServer(t)
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	r, source, _ := lookupResolver(t)
	source.current.Endpoint, source.current.Port = u.Hostname(), port
	source.current.UseSSL, source.current.PathStyle = false, true
	selected, err := r.Default(context.Background())
	require.NoError(t, err)
	legacy, err := NewS3(config.Storage{Endpoint: server.URL, Bucket: selected.Bucket,
		AccessKey: source.current.AccessKey, SecretKey: source.current.SecretKey})
	require.NoError(t, err)
	for name, object := range map[string]*S3{"resolver": selected.Object, "legacy-default": legacy} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			body := []byte("item,quantity\nbook,2\n路径,3\n")
			key := "tenants/1/shop/2026/11/lookup-文件.csv"
			n, err := object.Put(ctx, key, bytes.NewReader(body), int64(len(body)))
			require.NoError(t, err)
			require.EqualValues(t, len(body), n)
			file := FileObject{S3: object.WithBucket(selected.Bucket)}
			require.Same(t, object, file.S3)
			stream, _, size, err := file.Open(ctx, key)
			require.NoError(t, err)
			downloaded, err := io.ReadAll(stream)
			require.NoError(t, err)
			require.NoError(t, stream.Close())
			require.EqualValues(t, len(body), size)
			require.Equal(t, body, downloaded)
			signed, err := file.PresignGet(ctx, key)
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
			require.NoError(t, err)
			response, err := server.Client().Do(request)
			require.NoError(t, err)
			presigned, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, body, presigned)
			_, err = object.Get(ctx, "missing")
			require.EqualError(t, err, "export object unavailable")
		})
	}
}

func pathStorageServer(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			_, err := io.WriteString(w, "<LocationConstraint>us-east-1</LocationConstraint>")
			require.NoError(t, err)
			return
		}
		// A readable object need not grant the separate HEAD metadata request.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPut {
			var reader io.Reader = r.Body
			if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				reader = httputil.NewChunkedReader(reader)
			}
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			objects[r.URL.Path] = body
			w.Header().Set("ETag", `"lookup-etag"`)
			return
		}
		body, ok := objects[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, err := io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
			require.NoError(t, err)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"lookup-etag"`)
		if r.Method != http.MethodHead {
			_, err := w.Write(body)
			require.NoError(t, err)
		}
	}))
}
