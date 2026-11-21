package file

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestFileUploadPassesKnownSize(t *testing.T) {
	// BE-IDN-027: multipart readers use their explicit length; in-memory readers expose their remainder.
	data := []byte("known length\n")
	for _, test := range []struct {
		name string
		size int64
		body func() io.Reader
	}{
		{"multipart length", int64(len(data)), func() io.Reader { return struct{ io.Reader }{bytes.NewReader(data)} }},
		{"reader remainder", 0, func() io.Reader { return bytes.NewReader(data) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, objects, store := newFileTestService(t)
			view, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: "notes.txt", SizeBytes: test.size, Reader: test.body()})
			require.NoError(t, err)
			require.EqualValues(t, len(data), view.SizeBytes)
			require.Equal(t, data, objects.objects[store.rows[view.ID].ObjectKey])
		})
	}
}

func TestFileUploadRequiresSizeForStream(t *testing.T) {
	service, objects, store := newFileTestService(t)
	_, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: "notes.txt", Reader: struct{ io.Reader }{bytes.NewBufferString("unknown length")}})
	require.Error(t, err)
	require.Equal(t, 400, errs.From(err).HTTPStatus)
	require.Empty(t, objects.keys())
	require.Empty(t, store.rows)
}

func TestFileUploadIncludesThumbnailInIdempotentResponse(t *testing.T) {
	service, _, _ := newFileTestService(t)
	data := fileImage(t, "png", 640, 320)
	input := Input{TenantID: 1, UserID: 2, OriginalName: "photo.png", Reader: bytes.NewReader(data), SizeBytes: int64(len(data)), IdempotencyKey: "known-size-image"}
	view, first, err := service.Upload(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, view.ThumbURL)
	var response View
	require.NoError(t, json.Unmarshal(first.Body, &response))
	require.Equal(t, view, response)
	input.Reader = bytes.NewReader(data)
	_, replay, err := service.Upload(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, first, replay)
}
