package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"testing"
	"time"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/testenv"
	"github.com/stretchr/testify/require"
)

func TestFileThumbnailIntegrationRealPhoto(t *testing.T) {
	// BE-IDN-028 §4.2/3: explicit local acceptance using the same real photograph.
	data := thumbnailRealPhoto(t)
	f := newThumbnailFixture(t)
	started := time.Now()
	w, view := f.upload(t, "real-photo.jpg", "attachment", data, "")
	require.Equal(t, 201, w.Code)
	require.Equal(t, "pending", view.ThumbStatus)
	require.Nil(t, view.ThumbURL)
	t.Logf("#2 POST /files HTTP %d upload_ms=%d response=%s", w.Code, time.Since(started).Milliseconds(), w.Body.Bytes())
	assertThumbnailOutbox(t, f, view.ID, 1)
	cfg, publisher := thumbnailQueueFixture(t, f)
	startThumbnailRuntime(t, f, cfg, nil)
	dispatchThumbnailEvents(t, f, publisher)
	require.Eventually(t, func() bool {
		metadata, err := f.files.Metadata(f.ctx, 1, view.ID)
		return err == nil && metadata.ThumbStatus == "ready"
	}, 30*time.Second, 50*time.Millisecond)
	ready := assertThumbnailReady(t, f, view.ID)
	w, _ = f.request(t, "GET", fmt.Sprintf("/files/%d/metadata", ready.ID), nil, "")
	require.Equal(t, 200, w.Code)
	t.Logf("#3 same file_id=%d GET metadata HTTP %d total_ms=%d response=%s", ready.ID, w.Code, time.Since(started).Milliseconds(), w.Body.Bytes())
	w, envelope := f.request(t, "GET", "/files?keyword=real-photo", nil, "")
	require.Equal(t, 200, w.Code)
	var page filemodule.Page
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Len(t, page.List, 1)
	require.Equal(t, "ready", page.List[0].ThumbStatus)
	t.Logf("#3 GET list response=%s", envelope.Data)
	_, body, _, _, err := f.files.Get(f.ctx, 1, ready.ID)
	require.NoError(t, err)
	defer body.Close()
	read, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, data, read)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var attempts int
	require.NoError(t, q.Table("file_object").Select("thumb_attempts").Where("id=?", ready.ID).Scan(&attempts).Error)
	require.Zero(t, attempts)
}

func thumbnailRealPhoto(t *testing.T) []byte {
	t.Helper()
	path := testenv.Lookup("THUMB_PHOTO_PATH")
	if path == "" {
		t.Skip("real photograph acceptance requires THUMB_PHOTO_PATH")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 12_000_000)
	require.LessOrEqual(t, int64(len(data)), filemodule.MaxFileSize)
	config, kind, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, "jpeg", kind)
	require.LessOrEqual(t, int64(config.Width)*int64(config.Height), int64(40_000_000))
	digest := sha256.Sum256(data)
	t.Logf("real photograph bytes=%d dimensions=%dx%d pixels=%d sha256=%s", len(data), config.Width, config.Height, config.Width*config.Height, hex.EncodeToString(digest[:]))
	return data
}
