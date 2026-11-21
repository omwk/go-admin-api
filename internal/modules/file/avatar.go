package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
)

// MaxAvatarSize is the upload limit before image decoding or cropping.
const MaxAvatarSize int64 = 2 << 20

// AvatarStore resolves a public user identity and its most recent owned avatar.
type AvatarStore interface {
	UserAvatar(context.Context, int64) (int64, string, error)
	LatestAvatar(context.Context, int64, int64) (model.FileObject, error)
}

// AvatarProfiles keeps profile writes inside the file transaction and invalidation after commit.
type AvatarProfiles struct {
	Save       func(context.Context, int64, int64, string) error
	Invalidate func(context.Context, int64, int64) bool
}

// AvatarView is the stable internal profile address and its registered file ID.
type AvatarView struct {
	Avatar string `json:"avatar"`
	FileID int64  `json:"file_id"`
}

// UploadAvatar crops a decoded image and atomically registers its profile path.
func (s *Service) UploadAvatar(ctx context.Context, input Input, profiles AvatarProfiles) (AvatarView, idem.Result, error) {
	if input.Reader == nil || profiles.Save == nil {
		return AvatarView{}, idem.Result{}, invalidFile()
	}
	data, err := io.ReadAll(io.LimitReader(input.Reader, MaxAvatarSize+1))
	if err != nil {
		return AvatarView{}, idem.Result{}, invalidFile()
	}
	if int64(len(data)) > MaxAvatarSize {
		return AvatarView{}, idem.Result{}, avatarTooLarge()
	}
	if !strings.HasPrefix(fileMIME(input.OriginalName, data), "image/") {
		return AvatarView{}, idem.Result{}, invalidAvatar()
	}
	ctxImage, cancel := context.WithTimeout(ctx, thumbnailBudget)
	defer cancel()
	image, err := renderImage(ctxImage, data, 256, true)
	if err != nil {
		return AvatarView{}, idem.Result{}, invalidAvatar()
	}
	path := fmt.Sprintf("/api/v1/avatars/%d", input.UserID)
	input.Source, input.OriginalName, input.DeclaredMIME = "avatar", "avatar.jpg", "image/jpeg"
	input.Reader, input.SizeBytes = bytes.NewReader(image), int64(len(image))
	input.OnRegister = func(tx context.Context) error { return profiles.Save(tx, input.TenantID, input.UserID, path) }
	serialize := input.Serialize
	input.Serialize = func(status int, value any) (idem.Result, error) {
		avatar := AvatarView{Avatar: path, FileID: value.(View).ID}
		if serialize != nil {
			return serialize(status, avatar)
		}
		return idem.JSONResult(status, avatar)
	}
	view, result, err := s.Upload(ctx, input)
	return AvatarView{Avatar: path, FileID: view.ID}, result, err
}

// Avatar reads a private object through the application, or preserves a legacy external URL.
func (s *Service) Avatar(ctx context.Context, user int64) (io.ReadCloser, int64, string, error) {
	store, ok := s.store.(AvatarStore)
	if !ok || user <= 0 {
		return nil, 0, "", ErrFileNotFound
	}
	tenant, path, err := store.UserAvatar(ctx, user)
	if err != nil {
		return nil, 0, "", err
	}
	if path != fmt.Sprintf("/api/v1/avatars/%d", user) {
		u, err := url.Parse(path)
		if err == nil && u.Host != "" && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") {
			return nil, 0, path, nil
		}
		return nil, 0, "", ErrFileNotFound
	}
	row, err := store.LatestAvatar(ctx, tenant, user)
	if err != nil {
		return nil, 0, "", err
	}
	bound, err := s.bound(ctx, row.ProviderID, row.Bucket)
	if err != nil {
		return nil, 0, "", err
	}
	body, _, size, err := bound.object.Open(ctx, row.ObjectKey)
	return body, size, "", err
}

func invalidAvatar() error {
	return errs.New(errs.ErrInvalidParam, 400, "头像仅支持可解码的 JPEG、PNG、GIF 图片，最多 400 万像素")
}
func avatarTooLarge() error { return errs.New(errs.ErrInvalidParam, 413, "头像超过 2 MB 上限") }
