package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestFileImageThumbnailsAndOriginalBytes(t *testing.T) {
	// BE-IDN-025 #7: each supported decoder yields a bounded JPEG without changing the original.
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			service, objects, store := newFileTestService(t)
			data := fileImage(t, format, 640, 320)
			view, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: "photo." + format, Reader: bytes.NewReader(data)})
			require.NoError(t, err)
			row := store.rows[view.ID]
			require.NotEmpty(t, row.ThumbKey)
			config, kind, err := image.DecodeConfig(bytes.NewReader(objects.objects[row.ThumbKey]))
			require.NoError(t, err)
			require.Equal(t, "jpeg", kind)
			require.Equal(t, 320, config.Width)
			require.Equal(t, 160, config.Height)
			require.Equal(t, data, objects.objects[row.ObjectKey])
		})
	}
}

func TestFileImageWebPAndLargeImageFallback(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"pixel.webp", webp}, {"large.png", fileImage(t, "png", 2048, 2048)},
	} {
		service, objects, store := newFileTestService(t)
		view, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: test.name, Reader: bytes.NewReader(test.data)})
		require.NoError(t, err)
		require.Equal(t, "image", view.Category)
		require.Empty(t, store.rows[view.ID].ThumbKey)
		require.Len(t, objects.keys(), 1)
	}
}

func TestFileAvatarCropValidationAndProfileCallback(t *testing.T) {
	service, objects, store := newFileTestService(t)
	saved := false
	profiles := AvatarProfiles{Save: func(_ context.Context, tenant, user int64, path string) error {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 2, user)
		require.Equal(t, "/api/v1/avatars/2", path)
		saved = true
		return nil
	}}
	view, _, err := service.UploadAvatar(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: "photo.png", Reader: bytes.NewReader(fileImage(t, "png", 640, 320))}, profiles)
	require.NoError(t, err)
	require.True(t, saved)
	row := store.rows[view.FileID]
	require.Equal(t, "avatar", row.Purpose)
	decoded, err := jpeg.Decode(bytes.NewReader(objects.objects[row.ObjectKey]))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 256, 256), decoded.Bounds())
	// Red edge bands were cropped away; the center remains blue after JPEG encoding.
	r, _, b, _ := decoded.At(0, 128).RGBA()
	require.Greater(t, b, r)
	for _, test := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"notes.txt", []byte("plain text"), 400}, {"oversize.png", bytes.Repeat([]byte("x"), int(MaxAvatarSize)+1), 413},
		{"bad.png", []byte("\x89PNG\r\n\x1a\ninvalid"), 400},
	} {
		_, _, err := service.UploadAvatar(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: test.name, Reader: bytes.NewReader(test.data)}, profiles)
		require.Error(t, err)
		require.Equal(t, test.status, errs.From(err).HTTPStatus)
	}
}

func TestFileImageCanceledGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := renderImage(ctx, fileImage(t, "png", 640, 320), 320, false)
	require.ErrorIs(t, err, context.Canceled)
}

func fileImage(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}})
	for y := 0; y < height; y++ {
		for x := width / 4; x < width*3/4; x++ {
			img.SetColorIndex(x, y, 1)
		}
	}
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
