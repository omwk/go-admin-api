package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func TestFileStorageIntegrationMinIOUploadLimitsAndMIME(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	for _, item := range []struct {
		name string
		body []byte
	}{
		{"photo.jpg", []byte{255, 216, 255, 224, 0, 16, 'J', 'F', 'I', 'F', 0}},
		{"animation.gif", []byte("GIF89a-image")},
		{"document.pdf", []byte("%PDF-1.7\n%%EOF")},
		{"table.csv", []byte("one,two\n1,2\n")},
		{"sheet.xlsx", []byte("PK\x03\x04xlsx")},
		{"archive.zip", []byte("PK\x03\x04zip")},
	} {
		w, file := f.upload(t, item.name, "attachment", item.body, "")
		require.Equal(t, 201, w.Code, item.name)
		require.NotZero(t, file.ID)
	}
	maximum := append([]byte{255, 216, 255}, bytes.Repeat([]byte("a"), int(filemodule.MaxFileSize)-3)...)
	w, file := f.upload(t, "limit.jpg", "snapshot", maximum, "")
	require.Equal(t, 201, w.Code)
	require.Equal(t, filemodule.MaxFileSize, file.SizeBytes)
	w, _ = f.upload(t, "oversize.jpg", "attachment", append(maximum, 'x'), "")
	require.Equal(t, 413, w.Code)
	w, _ = f.upload(t, "script.js", "attachment", []byte("alert(1)"), "")
	require.Equal(t, 400, w.Code)
	w, _ = f.upload(t, "spoof.png", "attachment", []byte("PK\x03\x04zip"), "")
	require.Equal(t, 400, w.Code)
	w, response := f.request(t, "GET", "/files?purpose=snapshot&keyword=limit", nil, "")
	require.Equal(t, 200, w.Code)
	var page filemodule.Page
	require.NoError(t, json.Unmarshal(response.Data, &page))
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List, 1)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var audits int64
	require.NoError(t, q.Table("audit_log").Where("action='file.upload'").Count(&audits).Error)
	require.EqualValues(t, 7, audits)
	t.Log("acceptance 9: real MinIO: all six MIME families accepted; exact 20MiB=201; 20MiB+1=413; forbidden/spoofed MIME=400; filtered page and upload audit verified")
	for _, row := range page.List {
		w, _ = f.request(t, "DELETE", fmt.Sprintf("/files/%d", row.ID), nil, "")
		require.Equal(t, 204, w.Code)
	}
}
