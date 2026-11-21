package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationKnownSizeRoundTrip(t *testing.T) {
	// BE-IDN-027: real S3 storage must retain every byte of originals and generated images.
	f := newFilePutSizeFixture(t)
	server := httptest.NewServer(f.router)
	t.Cleanup(server.Close)
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			data := fileModelImage(t, format)
			var view filemodule.View
			filePutSizeUpload(t, f, server.URL, "/api/v1/admin/files", "photo."+format, data, &view)
			cleanupFilePutSize(t, f, view.ID)
			link := fileModelLink(t, f, view.ID)
			status, body, _ := fileModelGet(f.ctx, t, link.URL)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, data, body)
			require.EqualValues(t, len(data), view.SizeBytes)
			require.NotNil(t, view.ThumbURL)
			status, thumbnail, _ := fileModelGet(f.ctx, t, *view.ThumbURL)
			require.Equal(t, http.StatusOK, status)
			filePutSizeDimensions(t, thumbnail, 320, 160)
			row, err := newFileStore(f.database).File(f.ctx, 1, view.ID)
			require.NoError(t, err)
			require.Equal(t, filePutSizeObject(t, f, row.ThumbKey), thumbnail)
			if format == "png" {
				require.Equal(t, filePutSizeJPEG(t, 320, 160, true), thumbnail)
			}
			u, err := url.Parse(*view.ThumbURL)
			require.NoError(t, err)
			u.RawQuery = ""
			t.Logf("GET %s [signature omitted] -> HTTP %d; JPEG=320x160 bytes=%d sha256=%x; original HTTP 200 bytes=%d equal=true", u, status, len(thumbnail), sha256.Sum256(thumbnail), len(data))
		})
	}
	filePutSizeFallbacks(t, f, server.URL)
	t.Run("avatar", func(t *testing.T) { filePutSizeAvatar(t, f, server.URL) })
}

func filePutSizeFallbacks(t *testing.T, f *fileStorageFixture, base string) {
	t.Helper()
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	for _, sample := range []fileTypesSample{{"pixel.webp", webp}, {"notes.txt", []byte("BE-IDN-027 plain text\n")}, {"document.pdf", []byte("%PDF-1.7\n%%EOF")}} {
		t.Run(sample.name, func(t *testing.T) {
			var view filemodule.View
			filePutSizeUpload(t, f, base, "/api/v1/admin/files", sample.name, sample.body, &view)
			cleanupFilePutSize(t, f, view.ID)
			require.Nil(t, view.ThumbURL)
			status, body, _ := fileModelGet(f.ctx, t, fileModelLink(t, f, view.ID).URL)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, sample.body, body)
			t.Logf("%s: upload=201 thumb_url=null GET=200 bytes=%d equal=true", sample.name, len(body))
		})
	}
}

func filePutSizeAvatar(t *testing.T, f *fileStorageFixture, base string) {
	t.Helper()
	var avatar filemodule.AvatarView
	filePutSizeUpload(t, f, base, "/api/v1/auth/profile/avatar", "avatar.png", fileModelImage(t, "png"), &avatar)
	cleanupFilePutSize(t, f, avatar.FileID)
	status, body, _ := fileModelGet(f.ctx, t, base+avatar.Avatar)
	require.Equal(t, http.StatusOK, status)
	filePutSizeDimensions(t, body, 256, 256)
	require.Equal(t, filePutSizeJPEG(t, 256, 256, false), body)
	row, err := newFileStore(f.database).File(f.ctx, 1, avatar.FileID)
	require.NoError(t, err)
	require.Equal(t, filePutSizeObject(t, f, row.ObjectKey), body)
	require.EqualValues(t, len(body), row.SizeBytes)
	t.Logf("GET %s -> HTTP %d; JPEG=256x256 bytes=%d sha256=%x equal=true", avatar.Avatar, status, len(body), sha256.Sum256(body))
}

func newFilePutSizeFixture(t *testing.T) *fileStorageFixture {
	t.Helper()
	f := newFileStorageFixture(t)
	if *storageLookupConfig == "" {
		f.useMinIO(t)
	} else {
		p := liveLookupProvider(t, *storageLookupConfig)
		f.input.Endpoint, f.input.Port, f.input.Bucket = p.Endpoint, &p.Port, p.Bucket
		f.input.UseSSL, f.input.Region = &p.UseSSL, p.Region
		f.input.AccessKey, f.input.SecretKey = p.AccessKey, p.SecretKey
		t.Logf("BE-IDN-027 live endpoint=%s bucket=%s; development provider read only; database isolated", p.Endpoint, p.Bucket)
	}
	f.input.Name, f.input.AccessMode = "BE-IDN-027 acceptance", "private"
	f.save(t, f.input, 0, "")
	return f
}

func filePutSizeUpload(t *testing.T, f *fileStorageFixture, base, path, name string, data []byte, out any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, base+path, &body)
	require.NoError(t, err)
	request.Header.Set("Content-Type", mw.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+f.bearer)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	require.NoError(t, err)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	var envelope struct {
		Code int
		Data json.RawMessage
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	t.Logf("POST %s multipart file=%s bytes=%d -> HTTP %d code=%d", path, name, len(data), response.StatusCode, envelope.Code)
	require.Equal(t, http.StatusCreated, response.StatusCode, "response code=%d", envelope.Code)
	require.Zero(t, envelope.Code)
	require.NoError(t, json.Unmarshal(envelope.Data, out))
	if view, ok := out.(*filemodule.View); ok && view.ThumbURL != nil {
		u, err := url.Parse(*view.ThumbURL)
		require.NoError(t, err)
		u.RawQuery = ""
		t.Logf("response: id=%d mime=%s size_bytes=%d thumb_url=%s [signature omitted]", view.ID, view.MIME, view.SizeBytes, u)
	}
}

func filePutSizeDimensions(t *testing.T, data []byte, width, height int) {
	t.Helper()
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.Equal(t, width, config.Width)
	require.Equal(t, height, config.Height)
}

func filePutSizeJPEG(t *testing.T, width, height int, bands bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	blue := image.NewUniform(color.RGBA{20, 30, 200, 255})
	draw.Draw(img, img.Bounds(), blue, image.Point{}, draw.Src)
	if bands {
		red := image.NewUniform(color.RGBA{200, 30, 20, 255})
		draw.Draw(img, image.Rect(0, 0, width/4, height), red, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(width*3/4, 0, width, height), red, image.Point{}, draw.Src)
	}
	var buffer bytes.Buffer
	require.NoError(t, jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 80}))
	return buffer.Bytes()
}

func filePutSizeObject(t *testing.T, f *fileStorageFixture, key string) []byte {
	t.Helper()
	selected, err := f.resolver.Default(f.ctx)
	require.NoError(t, err)
	object, err := selected.Object.Get(f.ctx, key)
	require.NoError(t, err)
	data, err := io.ReadAll(object.Body)
	require.NoError(t, err)
	require.NoError(t, object.Body.Close())
	require.EqualValues(t, len(data), object.Size)
	return data
}

func cleanupFilePutSize(t *testing.T, f *fileStorageFixture, id int64) {
	t.Helper()
	row, err := newFileStore(f.database).File(f.ctx, 1, id)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), 30*time.Second)
		defer cancel()
		selected, err := f.resolver.Resolve(ctx, row.ProviderID)
		require.NoError(t, err)
		for _, key := range []string{row.ObjectKey, row.ThumbKey} {
			if key == "" {
				continue
			}
			require.NoError(t, selected.Object.WithBucket(row.Bucket).Delete(ctx, key))
			_, err := selected.Object.WithBucket(row.Bucket).Get(ctx, key)
			require.Error(t, err, "acceptance object must be removed")
		}
		w, _ := f.request(t, http.MethodDelete, "/files/"+strconv.FormatInt(id, 10), nil, "")
		require.Equal(t, http.StatusNoContent, w.Code)
		t.Logf("BE-IDN-027 cleanup: file_id=%d objects removed and confirmed absent; metadata DELETE=204; isolated database and audits removed by fixture", id)
	})
}
