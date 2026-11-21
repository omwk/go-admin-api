package bootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"net/url"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func TestFileModelIntegrationCategoryFilterAndLegacy(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	expected := map[string]string{"pixel.png": "image", "document.pdf": "document", "table.csv": "sheet", "sheet.xlsx": "sheet", "archive.zip": "archive", "notes.txt": "text", "data.json": "text"}
	var first int64
	for _, sample := range fileTypesSamples(t) {
		category, ok := expected[sample.name]
		if !ok {
			continue
		}
		w, view := f.upload(t, sample.name, "avatar", sample.body, "")
		require.Equal(t, 201, w.Code)
		require.Equal(t, category, view.Category)
		require.Equal(t, "attachment", view.Source)
		if first == 0 {
			first = view.ID
		}
		t.Logf("BE-IDN-025 #1: %s upload=201 category=%s source=attachment", sample.name, view.Category)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("file_object").Where("id=?", first).Updates(map[string]any{"category": nil, "thumb_key": nil}).Error)
	for _, category := range []string{"image", "document", "sheet", "archive", "text", "other"} {
		w, envelope := f.request(t, "GET", "/files?category="+category+"&purpose=avatar", nil, "")
		require.Equal(t, 200, w.Code)
		var page filemodule.Page
		require.NoError(t, json.Unmarshal(envelope.Data, &page))
		for _, view := range page.List {
			require.Equal(t, category, view.Category)
			require.Equal(t, f.actor.User.Username, view.UploadedByName)
		}
		counts := map[string]int{"image": 1, "document": 1, "sheet": 2, "archive": 1, "text": 2, "other": 0}
		require.EqualValues(t, counts[category], page.Total)
	}
	w, envelope := f.request(t, "GET", "/files?purpose=does-not-filter", nil, "")
	require.Equal(t, 200, w.Code)
	var page filemodule.Page
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.EqualValues(t, 7, page.Total)
	view, err := f.files.Metadata(f.ctx, 1, first)
	require.NoError(t, err)
	require.NotEmpty(t, view.Category)
	require.Nil(t, view.ThumbURL)
	t.Log("BE-IDN-025 #2/#10: all category filters exact; purpose ignored; NULL category/thumb_key projected and filtered from MIME")
}

func TestFileModelIntegrationPublicLinksAndThumbnails(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.input.AccessMode = ""
	p := f.save(t, f.input, 0, "")
	require.Equal(t, "public", p.AccessMode)
	require.EqualValues(t, 1024, p.QuotaPerUserMB)
	fileModelPolicy(t, f, true)
	for _, format := range []string{"png", "jpeg", "gif"} {
		w, view := f.upload(t, "preview."+format, "attachment", fileModelImage(t, format), "")
		require.Equal(t, 201, w.Code)
		w, envelope := f.request(t, "GET", fmt.Sprintf("/files/%d/url", view.ID), nil, "")
		require.Equal(t, 200, w.Code)
		var link filemodule.Link
		require.NoError(t, json.Unmarshal(envelope.Data, &link))
		require.Zero(t, link.ExpiresIn)
		u, err := url.Parse(link.URL)
		require.NoError(t, err)
		require.Empty(t, u.RawQuery)
		status, _, _ := fileModelGet(f.ctx, t, link.URL)
		require.Equal(t, 200, status)
		metadata, err := f.files.Metadata(f.ctx, 1, view.ID)
		require.NoError(t, err)
		require.NotNil(t, metadata.ThumbURL)
		status, body, _ := fileModelGet(f.ctx, t, *metadata.ThumbURL)
		require.Equal(t, 200, status)
		config, kind, err := image.DecodeConfig(bytes.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, "jpeg", kind)
		require.Equal(t, 320, config.Width)
		require.Equal(t, 160, config.Height)
		t.Logf("BE-IDN-025 #3/#7: original bare GET=200 expires_in=0; thumbnail GET=200 JPEG=%dx%d URL=%s", config.Width, config.Height, *metadata.ThumbURL)
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	require.NoError(t, err)
	w, view := f.upload(t, "pixel.webp", "attachment", webp, "")
	require.Equal(t, 201, w.Code)
	metadata, err := f.files.Metadata(f.ctx, 1, view.ID)
	require.NoError(t, err)
	require.Nil(t, metadata.ThumbURL)
	t.Log("BE-IDN-025 #7: WebP category=image thumb_url=null; original bytes retained")
}

func TestFileModelIntegrationPrivateThumbnail(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	w, view := f.upload(t, "private.png", "attachment", fileModelImage(t, "png"), "")
	require.Equal(t, 201, w.Code)
	metadata, err := f.files.Metadata(f.ctx, 1, view.ID)
	require.NoError(t, err)
	require.NotNil(t, metadata.ThumbURL)
	u, err := url.Parse(*metadata.ThumbURL)
	require.NoError(t, err)
	require.NotEmpty(t, u.Query().Get("X-Amz-Signature"))
	require.Equal(t, "300", u.Query().Get("X-Amz-Expires"))
	require.Empty(t, u.Query().Get("response-content-disposition"))
	status, body, _ := fileModelGet(f.ctx, t, *metadata.ThumbURL)
	require.Equal(t, 200, status)
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.Equal(t, 320, config.Width)
	require.Equal(t, 160, config.Height)
	u.RawQuery = ""
	status, _, _ = fileModelGet(f.ctx, t, u.String())
	require.Equal(t, 403, status)
	t.Log("BE-IDN-025 #7: private thumbnail signed GET=200 JPEG=320x160; bare GET=403; expires_in=300")
}

func TestFileModelIntegrationQuotaUsageAndDeleteRelease(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	quota := int64(1)
	f.input.QuotaPerUserMB = &quota
	f.save(t, f.input, 0, "")
	data := bytes.Repeat([]byte("a"), 600<<10)
	w, view := f.upload(t, "first.txt", "attachment", data, "quota-first")
	require.Equal(t, 201, w.Code)
	w, replay := f.upload(t, "first.txt", "attachment", data, "quota-first")
	require.Equal(t, 201, w.Code)
	require.Equal(t, view.ID, replay.ID)
	w, _ = f.upload(t, "second.txt", "attachment", bytes.Repeat([]byte("b"), 600<<10), "quota-second")
	require.Equal(t, 413, w.Code)
	require.Contains(t, w.Body.String(), "已用 0 MB / 上限 1 MB")
	t.Logf("BE-IDN-025 #5: second upload=413 %s", w.Body.String())
	w, envelope := f.request(t, "GET", "/files/usage", nil, "")
	require.Equal(t, 200, w.Code)
	var usage filemodule.Usage
	require.NoError(t, json.Unmarshal(envelope.Data, &usage))
	require.Equal(t, filemodule.Usage{UsedBytes: int64(len(data)), QuotaBytes: 1 << 20, FileCount: 1}, usage)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var sum int64
	require.NoError(t, q.Table("file_object").Select("SUM(size_bytes)").Where("tenant_id=1 AND uploaded_by=?", f.actor.User.ID).Scan(&sum).Error)
	require.Equal(t, sum, usage.UsedBytes)
	w, _ = f.request(t, "DELETE", fmt.Sprintf("/files/%d", view.ID), nil, "")
	require.Equal(t, 204, w.Code)
	w, _ = f.upload(t, "second.txt", "attachment", bytes.Repeat([]byte("b"), 600<<10), "quota-second")
	require.Equal(t, 201, w.Code)
	t.Logf("BE-IDN-025 #5/#6: usage=%+v SQL sum=%d; delete=204; next upload=201", usage, sum)
}

func TestFileModelIntegrationConcurrentQuota(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	quota := int64(1)
	f.input.QuotaPerUserMB = &quota
	f.save(t, f.input, 0, "")
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, char := range []byte{'a', 'b'} {
		go func(value byte) {
			<-start
			_, _, err := f.files.Upload(f.ctx, filemodule.Input{TenantID: 1, UserID: f.actor.User.ID, OriginalName: "concurrent.txt", Reader: bytes.NewReader(bytes.Repeat([]byte{value}, 600<<10))})
			results <- err
		}(char)
	}
	close(start)
	passed, rejected := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			passed++
		} else {
			require.Equal(t, 413, errs.From(err).HTTPStatus)
			rejected++
		}
	}
	require.Equal(t, 1, passed)
	require.Equal(t, 1, rejected)
	usage, err := f.files.Usage(context.WithoutCancel(f.ctx), 1, f.actor.User.ID)
	require.NoError(t, err)
	require.EqualValues(t, 600<<10, usage.UsedBytes)
	t.Log("BE-IDN-025 quota concurrency: two 600KiB uploads into 1MiB allowance -> one committed, one 413; used=614400")
}
