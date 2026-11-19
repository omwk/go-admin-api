package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
)

var _ repo.EmailChange = (*accountStore)(nil)

func (s *accountStore) EmailChangeByHash(ctx context.Context, hash string) (model.AuthToken, bool, error) {
	q, cancel := s.query(ctx)
	defer cancel()
	var row model.AuthToken
	// Only the globally unique capability selects the tenant; consumption rereads under its account lock.
	err := q.Select(lifecycleColumns).Where("purpose='email_change' AND token_hash=?", hash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *accountStore) RevokeEmailChangeTokens(ctx context.Context, tenant, user int64, now time.Time) error {
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Model(&model.AuthToken{}).Where("tenant_id=? AND user_id=? AND purpose='email_change' AND used_at IS NULL AND revoked_at IS NULL", tenant, user).
		Update("revoked_at", now).Error
}
