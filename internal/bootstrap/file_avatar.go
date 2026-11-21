package bootstrap

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	filemodule "github.com/omwk/go-admin-api/internal/modules/file"
	"github.com/omwk/go-admin-api/internal/modules/file/model"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"gorm.io/gorm"
)

func registerAvatarRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *filemodule.Service, database *db.DB) error {
	accounts := &identity.AccountService{Store: &accountStore{newAuthStore(database)}, Auth: auth}
	return filemodule.RegisterAvatarRoutes(router, auth, profiles, service, filemodule.AvatarProfiles{Save: accounts.SetAvatar, Invalidate: profiles.InvalidateUser})
}

func (s *fileStore) UserAvatar(ctx context.Context, user int64) (int64, string, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct {
		TenantID int64
		Avatar   string
	}
	err := q.Table("sys_user").Select("tenant_id,COALESCE(avatar,'') AS avatar").Where("id=? AND deleted_at IS NULL AND status='active'", user).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = filemodule.ErrFileNotFound
	}
	return row.TenantID, row.Avatar, err
}

func (s *fileStore) LatestAvatar(ctx context.Context, tenant, user int64) (model.FileObject, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row model.FileObject
	err := fileReadProjection(q).Where("file_object.tenant_id=? AND file_object.uploaded_by=? AND file_object.purpose='avatar'", tenant, user).Order("file_object.id DESC").Take(&row).Error
	return row, fileDBError(err)
}
