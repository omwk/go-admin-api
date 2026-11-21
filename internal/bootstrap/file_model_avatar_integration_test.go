package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/catalog/port"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/stretchr/testify/require"
)

func TestFileModelIntegrationAvatarModesAndLimits(t *testing.T) {
	for _, mode := range []string{"public", "private"} {
		t.Run(mode, func(t *testing.T) {
			f := newFileStorageFixture(t)
			f.useMinIO(t)
			f.input.AccessMode = mode
			f.save(t, f.input, 0, "")
			fileModelPolicy(t, f, mode == "public")
			profiles := cachedAdminRoutes(t, f.adminFixture)
			require.NoError(t, filemodule.RegisterFileRoutes(f.router, f.identityFixture.service, profiles, f.files))
			require.NoError(t, registerAvatarRoutes(f.router, f.identityFixture.service, profiles, f.files, f.database))
			f.bearer = f.login(t).AccessToken
			w, _ := f.request(t, "GET", "/files", nil, "")
			require.Equal(t, 403, w.Code)
			fileModelMe(t, f) // Warm the profile before changing the avatar.
			data := fileModelImage(t, "png")
			w, view := fileModelAvatarUpload(t, f, "avatar.png", data, "avatar-idempotency")
			require.Equal(t, 201, w.Code)
			require.Equal(t, fmt.Sprintf("/api/v1/avatars/%d", f.user.ID), view.Avatar)
			replayed, replay := fileModelAvatarUpload(t, f, "avatar.png", data, "avatar-idempotency")
			require.Equal(t, 201, replayed.Code)
			require.Equal(t, view, replay)
			me := fileModelMe(t, f)
			require.Equal(t, view.Avatar, *me.User.Avatar)
			request := httptest.NewRequest("GET", view.Avatar, nil)
			response := httptest.NewRecorder()
			f.router.ServeHTTP(response, request)
			require.Equal(t, 200, response.Code)
			require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
			require.Equal(t, "public, max-age=3600", response.Header().Get("Cache-Control"))
			config, _, err := image.DecodeConfig(bytes.NewReader(response.Body.Bytes()))
			require.NoError(t, err)
			require.Equal(t, 256, config.Width)
			require.Equal(t, 256, config.Height)
			row, err := newFileStore(f.database).File(f.ctx, 1, view.FileID)
			require.NoError(t, err)
			require.Equal(t, "avatar", row.Purpose)
			require.Equal(t, f.user.ID, *row.UploadedBy)
			if mode == "private" {
				link := fileModelLink(t, f, view.FileID)
				u, err := url.Parse(link.URL)
				require.NoError(t, err)
				u.RawQuery = ""
				status, _, _ := fileModelGet(f.ctx, t, u.String())
				require.Equal(t, 403, status)
			}
			w, _ = fileModelAvatarUpload(t, f, "notes.txt", []byte("not an image"), "")
			require.Equal(t, 400, w.Code)
			w, _ = fileModelAvatarUpload(t, f, "large.png", bytes.Repeat([]byte("x"), int(filemodule.MaxAvatarSize)+1), "")
			require.Equal(t, 413, w.Code)
			q, cancel := f.database.WithContext(f.ctx)
			defer cancel()
			var count int64
			require.NoError(t, q.Table("file_object").Where("tenant_id=1 AND uploaded_by=? AND purpose='avatar'", f.user.ID).Count(&count).Error)
			require.EqualValues(t, 1, count)
			t.Logf("BE-IDN-025 #8/#9: %s mode; member without file permission upload=201; replay same file_id; /auth/me internal avatar; anonymous GET=200 JPEG=256x256 Cache-Control=public,max-age=3600; text=400; >2MiB=413", mode)
		})
	}
}

func fileModelMe(t *testing.T, f *fileStorageFixture) struct{ User struct{ Avatar *string } } {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+f.bearer)
	response := httptest.NewRecorder()
	f.router.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code)
	var envelope struct {
		Data struct{ User struct{ Avatar *string } }
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return envelope.Data
}

func TestFileModelIntegrationAvatarLegacyAndRollback(t *testing.T) {
	f := newFileStorageFixture(t)
	f.useMinIO(t)
	f.save(t, f.input, 0, "")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("sys_user").Where("tenant_id=1 AND id=?", f.user.ID).Update("avatar", "https://legacy.example/avatar.jpg").Error)
	request := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/avatars/%d", f.user.ID), nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, request)
	require.Equal(t, 302, w.Code)
	require.Equal(t, "https://legacy.example/avatar.jpg", w.Header().Get("Location"))
	_, _, err := f.files.UploadAvatar(f.ctx, filemodule.Input{TenantID: 1, UserID: f.user.ID, OriginalName: "avatar.png", Reader: bytes.NewReader(fileModelImage(t, "png"))}, filemodule.AvatarProfiles{Save: func(context.Context, int64, int64, string) error { return errors.New("profile write failure") }})
	require.EqualError(t, err, "profile write failure")
	var count int64
	require.NoError(t, q.Table("file_object").Count(&count).Error)
	require.Zero(t, count)
	var avatar string
	require.NoError(t, q.Table("sys_user").Select("avatar").Where("id=?", f.user.ID).Scan(&avatar).Error)
	require.Equal(t, "https://legacy.example/avatar.jpg", avatar)
	selected, err := f.resolver.Default(f.ctx)
	require.NoError(t, err)
	var objects int
	require.NoError(t, selected.Object.Scan(f.ctx, "tenants/1/avatar/", func(_ port.ObjectInfo) error { objects++; return nil }))
	require.Zero(t, objects)
	t.Log("BE-IDN-025 #8/#10: legacy avatar=302; profile failure rolls back file row and preserves the legacy URL")
}
