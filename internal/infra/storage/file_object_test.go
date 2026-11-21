package storage

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/stretchr/testify/require"
)

func TestFileObjectPutKnownSize(t *testing.T) {
	// BE-IDN-027: known lengths must use single-object PUT with the real SDK.
	server := pathStorageServer(t)
	t.Cleanup(server.Close)
	object, err := NewS3(config.Storage{Endpoint: server.URL, Bucket: "files"})
	require.NoError(t, err)
	file := FileObject{S3: object}
	for _, body := range [][]byte{[]byte("original PNG bytes"), bytes.Repeat([]byte("thumbnail"), 1024), bytes.Repeat([]byte("avatar"), 1024)} {
		key := "tenants/1/attachment/" + string(body[:6])
		reader := struct{ io.Reader }{bytes.NewReader(body)} // The size is explicit even without a seekable reader.
		created, err := file.Put(context.Background(), key, "image/jpeg", reader, int64(len(body)))
		require.NoError(t, err)
		require.True(t, created)
		stream, _, size, err := file.Open(context.Background(), key)
		require.NoError(t, err)
		actual, err := io.ReadAll(stream)
		require.NoError(t, err)
		require.NoError(t, stream.Close())
		require.EqualValues(t, len(body), size)
		require.Equal(t, body, actual)
	}
}

func TestFileUploadsDoNotPassUnknownSize(t *testing.T) {
	// BE-IDN-027: the independent streaming CSV exporter has its own multipart contract.
	unknownSize := regexp.MustCompile(`(?s)\.(?:PutFile|Put)\([^;{}]*,\s*-\s*1\s*\)`)
	for _, root := range []string{".", "../../modules/file"} {
		paths, err := filepath.Glob(filepath.Join(root, "*.go"))
		require.NoError(t, err)
		require.NotEmpty(t, paths)
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.False(t, unknownSize.Match(data), "unknown object size in %s", path)
		}
	}
}

func TestFilePresignForcesAttachment(t *testing.T) {
	// BE-IDN-023: the response override must be included before the SDK signs it.
	for _, pathStyle := range []bool{true, false} {
		p := lookupProvider(t)
		p.PathStyle = pathStyle
		object, err := NewProviderS3(p)
		require.NoError(t, err)
		for _, key := range []string{"tenants/1/attachment/drawing.svg", "tenants/1/attachment/notes.txt"} {
			signed, err := (FileObject{S3: object}).PresignGet(context.Background(), key)
			require.NoError(t, err)
			u, err := url.Parse(signed)
			require.NoError(t, err)
			require.Equal(t, "attachment", u.Query().Get("response-content-disposition"))
			require.Equal(t, "300", u.Query().Get("X-Amz-Expires"))
			require.NotEmpty(t, u.Query().Get("X-Amz-Signature"))
		}
	}
}
