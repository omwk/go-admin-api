package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
)

var _ repo.LoginTenant = (*authStore)(nil)

func (s *authStore) TenantForLogin(ctx context.Context, code string, id int64) (model.Tenant, bool, error) {
	if code == "" && id <= 0 {
		return model.Tenant{}, false, errors.New("login tenant selector is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	q = q.Select("id, code, status")
	if code != "" {
		q = q.Where("code = ?", code)
	} else {
		q = q.Where("id = ?", id)
	}
	var row model.Tenant
	err := q.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}
