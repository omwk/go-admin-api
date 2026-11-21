package bootstrap

import (
	"bytes"
	"encoding/json"
	"testing"

	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	filemodel "github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/stretchr/testify/require"
)

func TestFileLibraryIntegrationAvatarFilter(t *testing.T) {
	// BE-IDN-026 §4.1: filter before counting/paging and retain tenant isolation.
	f := newFileStorageFixture(t)
	store := newFileStore(f.database)
	for i, purpose := range []string{"attachment", "shop", "export", "snapshot", "avatar", "avatar"} {
		tenant := int64(1)
		if i == 5 {
			tenant = 2
		}
		row := filemodel.FileObject{TenantID: tenant, Bucket: "library", ObjectKey: purpose, OriginalName: purpose + ".jpg", MIME: "image/jpeg", SizeBytes: 100, SHA256: stringHex(t, 32), Purpose: purpose, UploadedBy: &f.actor.User.ID}
		require.NoError(t, store.CreateFile(f.ctx, &row))
	}
	for _, test := range []struct {
		query  string
		total  int64
		count  int
		avatar bool
	}{
		{"", 4, 4, false},
		{"?include_avatar=false", 4, 4, false},
		{"?include_avatar=true", 5, 5, true},
		{"?category=image&keyword=avatar", 0, 0, false},
		{"?category=image&keyword=avatar&include_avatar=true", 1, 1, true},
		{"?page_size=2&page=1", 4, 2, false},
		{"?page_size=2&page=2", 4, 2, false},
		{"?page_size=2&page=3", 4, 0, false},
	} {
		t.Run(test.query, func(t *testing.T) {
			w, envelope := f.request(t, "GET", "/files"+test.query, nil, "")
			require.Equal(t, 200, w.Code)
			var page filemodule.Page
			require.NoError(t, json.Unmarshal(envelope.Data, &page))
			require.Equal(t, test.total, page.Total)
			require.Len(t, page.List, test.count)
			var avatars int
			for _, file := range page.List {
				if file.Source == "avatar" {
					avatars++
				}
			}
			require.Equal(t, test.avatar, avatars == 1)
			t.Logf("GET /files%s: total=%d count=%d avatars=%d", test.query, page.Total, len(page.List), avatars)
		})
	}
	w, _ := f.request(t, "GET", "/files?include_avatar=invalid", nil, "")
	require.Equal(t, 400, w.Code)
}

func TestFileLibraryIntegrationAvatarUsageAndQuota(t *testing.T) {
	// BE-IDN-026 §4.2: a hidden avatar still consumes the uploader's allowance.
	f := newFileStorageFixture(t)
	quota := int64(1)
	f.input.QuotaPerUserMB = &quota
	f.save(t, f.input, 0, "")
	row := filemodel.FileObject{TenantID: 1, Bucket: "library", ObjectKey: "avatar.jpg", OriginalName: "avatar.jpg", MIME: "image/jpeg", SizeBytes: 1 << 20, SHA256: stringHex(t, 32), Purpose: "avatar", UploadedBy: &f.actor.User.ID}
	require.NoError(t, newFileStore(f.database).CreateFile(f.ctx, &row))
	w, envelope := f.request(t, "GET", "/files/usage", nil, "")
	require.Equal(t, 200, w.Code)
	var usage filemodule.Usage
	require.NoError(t, json.Unmarshal(envelope.Data, &usage))
	require.Equal(t, filemodule.Usage{UsedBytes: 1 << 20, QuotaBytes: 1 << 20, FileCount: 1}, usage)
	w, _ = f.upload(t, "over-quota.txt", "attachment", bytes.Repeat([]byte("a"), 1024), "")
	require.Equal(t, 413, w.Code)
	t.Logf("BE-IDN-026 usage=%+v including purpose=avatar; subsequent upload=413", usage)
}
