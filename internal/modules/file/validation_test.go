package file

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestFileUploadTextTypes(t *testing.T) {
	// BE-IDN-023: declared text aliases must not override the content sniff.
	for _, test := range []struct{ name, declared, body, sniffed string }{
		{"notes.txt", "text/plain; charset=utf-8", "plain text\n", "text/plain"},
		{"notes.md", "text/markdown", "# Heading\n", "text/plain"},
		{"data.json", "application/json", `{"ok":true}`, "text/plain"},
		{"events.log", "text/plain", "INFO started\n", "text/plain"},
		{"events.ndjson", "application/x-ndjson", "{\"n\":1}\n{\"n\":2}\n", "text/plain"},
		{"events.jsonl", "application/x-ndjson", "{\"n\":3}\n", "text/plain"},
		{"config.yaml", "application/yaml", "enabled: true\n", "text/plain"},
		{"config.yml", "text/yaml", "enabled: false\n", "text/plain"},
		{"data.xml", "text/xml", `<?xml version="1.0"?><data/>`, "text/xml"},
		{"other.xml", "application/xml", `<?xml version="1.0"?><other/>`, "text/xml"},
		{"bare.xml", "application/xml", "<data/>", "text/plain"},
		{"readme.md", "application/octet-stream", "# Read me\n", "text/plain"},
		{"undeclared.json", "", `{"declared":false}`, "text/plain"},
		{"generic.txt", "", "generic\n", "text/plain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, store := newFileTestService(t)
			view, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2,
				OriginalName: test.name, DeclaredMIME: test.declared, Reader: strings.NewReader(test.body)})
			require.NoError(t, err)
			require.Equal(t, test.sniffed, view.MIME)
			row, body, _, size, err := service.Get(context.Background(), 1, view.ID)
			require.NoError(t, err)
			defer body.Close()
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			require.Equal(t, test.body, string(data))
			require.EqualValues(t, len(test.body), size)
			require.Equal(t, test.sniffed, row.MIME)
			require.Regexp(t, `^tenants/1/attachment/\d{4}/\d{2}/[a-f0-9]{32}-`, store.rows[view.ID].ObjectKey)
		})
	}
}

func TestFileUploadTypeErrorsDescribeSniffedMIME(t *testing.T) {
	// BE-IDN-023: neither a text declaration nor its extension admits HTML or binary data.
	for _, test := range []struct{ name, declared, body, sniffed string }{
		{"spoof.txt", "text/plain", "<!DOCTYPE html><html><script>alert(1)</script></html>", "text/html"},
		{"spoof.json", "application/json", "<html>not JSON</html>", "text/html"},
		{"spoof.md", "text/markdown", "<script>alert(1)</script>", "text/html"},
		{"spoof.yaml", "application/yaml", "<html>not YAML</html>", "text/html"},
		{"spoof.xml", "application/xml", "<html>not XML</html>", "text/html"},
		{"binary.txt", "text/plain", "\x00\x01\x02\x03", "application/octet-stream"},
		{"script.js", "text/javascript", "alert(1)", "text/plain"},
		{"script.exe", "application/octet-stream", "plain text", "text/plain"},
		{"image.png", "image/png", "plain text", "text/plain"},
		{"notes.txt", "text/html", "plain text", "text/plain"},
	} {
		t.Run(test.name+"_"+test.declared, func(t *testing.T) {
			service, objects, store := newFileTestService(t)
			_, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2,
				OriginalName: test.name, DeclaredMIME: test.declared, Reader: strings.NewReader(test.body)})
			require.Error(t, err)
			app := errs.From(err)
			require.Equal(t, http.StatusBadRequest, app.HTTPStatus)
			require.Equal(t, errs.ErrInvalidParam, app.Code)
			require.Equal(t, "不支持的文件类型："+test.sniffed, app.Message)
			require.Equal(t, test.sniffed, app.Details["mime"])
			require.Contains(t, app.Details["allowed"], "text/plain")
			require.NotContains(t, app.Details["allowed"], "text/html")
			require.Empty(t, objects.keys())
			require.Empty(t, store.rows)
		})
	}
}

func TestFileUploadParameterMessages(t *testing.T) {
	// BE-IDN-023: filename and size errors remain distinguishable from MIME errors.
	for _, test := range []struct {
		name, body, message string
		status              int
	}{
		{"", "text", "文件名不能为空", 400},
		{"   ", "text", "文件名不能为空", 400},
		{".", "text", "文件名不能为空", 400},
		{"empty.txt", "", "文件不能为空", 400},
		{"large.txt", strings.Repeat("x", int(MaxFileSize)+1), "文件超过 20 MB 上限", 413},
	} {
		t.Run(test.message+test.name, func(t *testing.T) {
			service, objects, _ := newFileTestService(t)
			_, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2,
				OriginalName: test.name, Reader: strings.NewReader(test.body)})
			require.Error(t, err)
			require.Equal(t, test.status, errs.From(err).HTTPStatus)
			require.Equal(t, test.message, errs.From(err).Message)
			require.Empty(t, errs.From(err).Details)
			require.Empty(t, objects.keys())
		})
	}
}

func TestFileUploadAcceptsExactTextSizeLimit(t *testing.T) {
	service, _, _ := newFileTestService(t)
	view, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2,
		OriginalName: "limit.txt", Reader: bytes.NewReader(bytes.Repeat([]byte("x"), int(MaxFileSize)))})
	require.NoError(t, err)
	require.Equal(t, MaxFileSize, view.SizeBytes)
}
