package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *authStore) CheckTenant(ctx context.Context, tenant int64) error {
	q, cancel := s.query(ctx)
	defer cancel()
	var row struct{ Status string }
	err := q.Table("tenant").Select("status").Where("id=?", tenant).
		Clauses(clause.Locking{Strength: "SHARE"}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errs.New(20101, 401, "用户名或密码错误")
	}
	if err != nil {
		return err
	}
	if row.Status != "active" {
		return errs.New(errs.ErrTenantSuspended, 403, "租户已停用")
	}
	return nil
}
