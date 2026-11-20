package bootstrap

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"testing"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func TestFileTypesIntegrationUploadsAndDownloads(t *testing.T) {
	// BE-IDN-023 #1-5: real storage, HTTP upload and byte-identical controlled download.
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	for _, test := range fileTypesSamples(t) {
		t.Run(test.name, func(t *testing.T) {
			w, view := fileTypesUpload(t, f, test.name, "application/octet-stream", test.body, false)
			require.Equal(t, 201, w.Code, w.Body.String())
			require.NotZero(t, view.ID)
			w, _ = f.request(t, "GET", fmt.Sprintf("/files/%d", view.ID), nil, "")
			require.Equal(t, 200, w.Code)
			require.Equal(t, test.body, w.Body.Bytes())
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			disposition, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			require.NoError(t, err)
			require.Equal(t, "attachment", disposition)
			require.Equal(t, test.name, params["filename"])
			digest := sha256.Sum256(w.Body.Bytes())
			require.Equal(t, hex.EncodeToString(digest[:]), view.SHA256)
			t.Logf("BE-IDN-023 #1-5: %s upload=201 download=200 identical=%t MIME=%s attachment+nosniff", test.name, bytes.Equal(test.body, w.Body.Bytes()), view.MIME)
		})
	}
}

func TestFileTypesIntegrationHTMLAndErrors(t *testing.T) {
	// BE-IDN-023 #6/#8/#9: HTTP errors include the sniffed type and actual whitelist.
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	w, _ := fileTypesUpload(t, f, "spoof.txt", "text/plain", []byte("<!DOCTYPE html><html><script>alert(1)</script></html>"), true)
	require.Equal(t, 400, w.Code)
	var rejected struct {
		Code    int
		Message string
		Details struct {
			MIME    string
			Allowed []string
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rejected))
	require.Equal(t, 10001, rejected.Code)
	require.Equal(t, "不支持的文件类型：text/html", rejected.Message)
	require.Equal(t, "text/html", rejected.Details.MIME)
	for _, allowed := range []string{"text/plain", "text/markdown", "application/json", "application/x-ndjson", "text/xml", "application/xml", "application/yaml", "text/yaml", "image/svg+xml"} {
		require.Contains(t, rejected.Details.Allowed, allowed)
	}
	require.NotContains(t, rejected.Details.Allowed, "text/html")
	require.NotContains(t, rejected.Details.Allowed, "text/javascript")
	require.NotContains(t, rejected.Details.Allowed, "application/octet-stream")
	maximum := bytes.Repeat([]byte("x"), int(filemodule.MaxFileSize))
	w, view := fileTypesUpload(t, f, "limit.txt", "text/plain", maximum, false)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, filemodule.MaxFileSize, view.SizeBytes)
	w, _ = fileTypesUpload(t, f, "oversize.txt", "text/plain", append(maximum, 'x'), false)
	require.Equal(t, 413, w.Code)
	require.Contains(t, w.Body.String(), `"message":"文件超过 20 MB 上限"`)
	t.Logf("BE-IDN-023 #8: exact 20971520 bytes=201; 20971521 bytes=%d %s", w.Code, w.Body.String())
	w, _ = fileTypesUpload(t, f, "", "text/plain", []byte("text"), false)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), `"message":"文件名不能为空"`)
	t.Logf("BE-IDN-023 filename: %d %s", w.Code, w.Body.String())
}

func TestFileTypesIntegrationSVGPresignedAttachment(t *testing.T) {
	// BE-IDN-023 #7: inspect the response of a real SDK-signed MinIO GET.
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	body := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	w, view := fileTypesUpload(t, f, "drawing.svg", "image/svg+xml", body, false)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Equal(t, "image/svg+xml", view.MIME)
	w, envelope := f.request(t, "GET", fmt.Sprintf("/files/%d/url", view.ID), nil, "")
	require.Equal(t, 200, w.Code)
	var signed struct {
		URL       string
		ExpiresIn int `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal(envelope.Data, &signed))
	require.Equal(t, 300, signed.ExpiresIn)
	u, err := url.Parse(signed.URL)
	require.NoError(t, err)
	require.Equal(t, "attachment", u.Query().Get("response-content-disposition"))
	request, err := http.NewRequestWithContext(f.ctx, http.MethodGet, signed.URL, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 200, response.StatusCode)
	require.Equal(t, "attachment", response.Header.Get("Content-Disposition"))
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, body, data)
	headers, err := httputil.DumpResponse(response, false)
	require.NoError(t, err)
	t.Logf("BE-IDN-023 #7: SVG upload=201; signed query response-content-disposition=attachment; expires_in=300; actual GET headers:\n%s", headers)
}

func fileTypesUpload(t *testing.T, f *fileStorageFixture, name, declared string, body []byte, dump bool) (*httptest.ResponseRecorder, filemodule.View) {
	t.Helper()
	var buffer bytes.Buffer
	mw := multipart.NewWriter(&buffer)
	require.NoError(t, mw.SetBoundary("BE-IDN-023"))
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": name}))
	header.Set("Content-Type", declared)
	part, err := mw.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(body)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	r := httptest.NewRequest("POST", "/api/v1/admin/files", &buffer)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if dump {
		// Capture the original request before adding authentication; never log a bearer token.
		raw, err := httputil.DumpRequest(r, true)
		require.NoError(t, err)
		t.Logf("BE-IDN-023 #6 request (Authorization omitted):\n%s", raw)
	}
	r.Header.Set("Authorization", "Bearer "+f.bearer)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	if dump {
		response := w.Result()
		raw, err := httputil.DumpResponse(response, true)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		t.Logf("BE-IDN-023 #6/#9 response:\n%s", raw)
	}
	envelope := f.envelope(t, "POST", "/files", w)
	var view filemodule.View
	if w.Code == 201 {
		require.NoError(t, json.Unmarshal(envelope.Data, &view))
	}
	return w, view
}

type fileTypesSample struct {
	name string
	body []byte
}

func fileTypesSamples(t *testing.T) []fileTypesSample {
	t.Helper()
	var pixel bytes.Buffer
	require.NoError(t, png.Encode(&pixel, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	return []fileTypesSample{
		{"notes.txt", []byte("plain text\n")},
		{"readme.md", []byte("# Read me\n")},
		{"data.json", []byte(`{"ok":true}`)},
		{"events.log", []byte("INFO started\n")},
		{"table.csv", []byte("item,quantity\nbook,2\n")},
		{"document.pdf", []byte("%PDF-1.7\n%%EOF")},
		{"pixel.png", pixel.Bytes()},
		{"archive.zip", fileTypesArchive(t, map[string]string{"notes.txt": "archived text"})},
		{"sheet.xlsx", fileTypesArchive(t, map[string]string{
			"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`,
			"xl/workbook.xml":     `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets/></workbook>`,
		})},
		{"config.yaml", []byte("enabled: true\n")},
		{"config.yml", []byte("enabled: false\n")},
		{"events.ndjson", []byte("{\"n\":1}\n{\"n\":2}\n")},
		{"data.xml", []byte(`<?xml version="1.0"?><data/>`)},
	}
}

func fileTypesArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for name, value := range entries {
		entry, err := zw.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(entry, value)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buffer.Bytes()
}
