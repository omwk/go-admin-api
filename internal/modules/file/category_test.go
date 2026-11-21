package file

import (
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/stretchr/testify/require"
)

func TestFileCategoryMIMEAndLegacyProjection(t *testing.T) {
	// BE-IDN-025 #1/#10: classification also applies to rows predating migration 39.
	for _, test := range []struct{ mime, category string }{
		{"image/png", "image"}, {"image/webp", "image"}, {"application/pdf", "document"},
		{"text/csv", "sheet"}, {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "sheet"},
		{"application/zip", "archive"}, {"text/plain; charset=utf-8", "text"},
		{"application/json", "text"}, {"application/xml", "text"}, {"application/yaml", "text"},
		{"application/octet-stream", "other"},
	} {
		t.Run(test.mime, func(t *testing.T) {
			view := projectFile(model.FileObject{MIME: test.mime, Purpose: "attachment"})
			require.Equal(t, test.category, view.Category)
			require.Equal(t, "attachment", view.Source)
			require.Nil(t, view.ThumbURL)
		})
	}
}
