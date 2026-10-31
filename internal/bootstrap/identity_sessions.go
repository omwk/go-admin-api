package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

type sessionStore struct{ admin *adminStore }

var _ repo.Sessions = (*sessionStore)(nil)

func (s *sessionStore) Sessions(ctx context.Context, tenant int64, page repo.Page, now time.Time) ([]repo.Session, int64, error) {
	q, cancel := s.admin.query(ctx, tenant, &model.RefreshToken{}, "Sessions.Sessions")
	defer cancel()
	// Every family subquery binds tenant explicitly; rotation history is retained.
	q = q.Where("sys_refresh_token.revoked_at IS NULL AND sys_refresh_token.expires_at > ?", now).
		Where(`NOT EXISTS (SELECT 1 FROM sys_refresh_token terminal WHERE terminal.tenant_id = ? AND terminal.session_id = sys_refresh_token.session_id AND terminal.user_id = sys_refresh_token.user_id AND terminal.revoked_reason IN ('logout','kick','replay'))`, tenant)
	var total int64
	if err := q.Fork().Distinct("sys_refresh_token.session_id").Count(&total); err != nil {
		return nil, 0, err
	}
	rows := []repo.Session{}
	err := q.Fork().Select("sys_refresh_token.session_id AS sid, sys_refresh_token.user_id, MIN(sys_refresh_token.created_at) AS created_at, MAX(sys_refresh_token.expires_at) AS expires_at").
		Group("sys_refresh_token.session_id, sys_refresh_token.user_id").Order("created_at desc, sid desc").Limit(page.Size).Offset((page.Number - 1) * page.Size).Find(&rows)
	return rows, total, err
}

func (s *sessionStore) SessionFamily(ctx context.Context, tenant int64, sid string) (repo.Family, error) {
	q, cancel := s.admin.query(ctx, tenant, &model.RefreshToken{}, "Sessions.SessionFamily")
	defer cancel()
	rows := []model.RefreshToken{}
	err := q.Select(refreshColumns).Where("session_id = ?", sid).Order("id").Find(&rows)
	if err != nil {
		return repo.Family{}, err
	}
	if len(rows) == 0 {
		return repo.Family{}, errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	for _, row := range rows {
		if row.UserID != rows[0].UserID {
			return repo.Family{}, errs.New(errs.ErrNotFound, 404, "资源不存在")
		}
	}
	return repo.Family{Tokens: rows}, nil
}
