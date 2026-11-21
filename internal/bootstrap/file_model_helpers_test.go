package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func fileModelPolicy(t *testing.T, f *fileStorageFixture, public bool) {
	t.Helper()
	u, err := url.Parse(f.input.Endpoint)
	require.NoError(t, err)
	client, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(f.input.AccessKey, f.input.SecretKey, ""), Secure: u.Scheme == "https"})
	require.NoError(t, err)
	policy := ""
	if public {
		raw, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Principal": map[string]any{"AWS": []string{"*"}}, "Action": []string{"s3:GetObject"}, "Resource": []string{"arn:aws:s3:::" + f.input.Bucket + "/*"}}}})
		require.NoError(t, err)
		policy = string(raw)
	}
	require.NoError(t, client.SetBucketPolicy(f.ctx, f.input.Bucket, policy))
}

func fileModelGet(ctx context.Context, t *testing.T, value string) (int, []byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, "GET", value, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.True(t, err == nil, "object GET failed; signed address omitted")
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, body, response.Header
}

func fileModelImage(t *testing.T, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 320))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{200, 30, 20, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(160, 0, 480, 320), image.NewUniform(color.RGBA{20, 30, 200, 255}), image.Point{}, draw.Src)
	var buffer bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buffer, img)
	case "jpeg":
		err = jpeg.Encode(&buffer, img, nil)
	case "gif":
		err = gif.Encode(&buffer, img, nil)
	}
	require.NoError(t, err)
	return buffer.Bytes()
}

func fileModelAvatarUpload(t *testing.T, f *fileStorageFixture, name string, data []byte, key string) (*httptest.ResponseRecorder, filemodule.AvatarView) {
	t.Helper()
	var buffer bytes.Buffer
	mw := multipart.NewWriter(&buffer)
	part, err := mw.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	request := httptest.NewRequest("POST", "/api/v1/auth/profile/avatar", &buffer)
	request.Header.Set("Content-Type", mw.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+f.bearer)
	request.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, request)
	validateOpenAPIResponse(t, readStorageContract(t, "avatar"), "/api/v1/auth/profile/avatar", "post", w.Code, w.Body.Bytes())
	var envelope struct{ Data filemodule.AvatarView }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	return w, envelope.Data
}

func fileModelLink(t *testing.T, f *fileStorageFixture, id int64) filemodule.Link {
	t.Helper()
	value, err := f.files.URL(f.ctx, 1, id)
	require.NoError(t, err)
	return value
}
