package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *profileStore) account(ctx context.Context, tenant, user int64) error {
	if tenant <= 0 || user <= 0 {
		return errs.New(20103, 401, "token 已失效")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var count int64
	err := q.Model(&model.User{}).Joins("JOIN tenant t ON t.id = sys_user.tenant_id AND t.status = ?", "active").
		Where("sys_user.tenant_id = ? AND sys_user.id = ? AND sys_user.status = ?", tenant, user, "active").Count(&count).Error
	if err != nil {
		return &errs.AppError{Code: errs.ErrInternal, HTTPStatus: 503, Message: "身份依赖不可用", Cause: err}
	}
	if count != 1 {
		return errs.New(20103, 401, "token 已失效")
	}
	return nil
}

func (s *adminStore) AffectedUsers(ctx context.Context, tenant int64, change repo.AuthzChange) ([]int64, error) {
	if err := requireAdminTenant(ctx, tenant, "Admin.AffectedUsers"); err != nil {
		return nil, err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	ids := []int64{}
	switch change.Resource {
	case "users":
		if change.ID > 0 {
			ids = append(ids, change.ID)
		}
	case "roles":
		if change.ID > 0 {
			err := q.Table("sys_user_role").Where("tenant_id = ? AND role_id = ?", tenant, change.ID).Distinct().Pluck("user_id", &ids).Error
			return ids, err
		}
	case "menus":
		// A shared ancestor's visibility can affect any granted descendant and super users.
		err := q.Model(&model.User{}).Where("tenant_id = ?", tenant).Pluck("id", &ids).Error
		return ids, err
	}
	return ids, nil
}
