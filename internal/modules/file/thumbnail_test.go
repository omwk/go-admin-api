package file

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileThumbnailUploadStates(t *testing.T) {
	// BE-IDN-028: uploaded originals survive unsupported/corrupt/large previews.
	for _, test := range []struct {
		name, status string
		data         []byte
	}{
		{"small.png", "ready", fileImage(t, "png", 640, 320)},
		{"pixels.png", "pending", fileImage(t, "png", 2048, 2048)},
		{"large.jpg", "pending", append(jpegHeader(), bytes.Repeat([]byte("x"), imageInputLimit)...)},
		{"broken.jpg", "failed", jpegHeader()},
		{"notes.txt", "", []byte("ordinary text")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, objects, store := newFileTestService(t)
			view, _, err := service.Upload(context.Background(), Input{TenantID: 1, UserID: 2, OriginalName: test.name, Reader: bytes.NewReader(test.data)})
			require.NoError(t, err)
			raw, err := json.Marshal(view)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(raw, &fields))
			require.Equal(t, test.status, fields["thumb_status"])
			require.Equal(t, test.status == "ready", view.ThumbURL != nil)
			require.Equal(t, test.data, objects.objects[store.rows[view.ID].ObjectKey])
		})
	}
}
