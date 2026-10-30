package bootstrap

import (
	"context"
	"errors"
)

func (s *authStore) SessionRevoked(ctx context.Context, tenant, user int64, sid string) (bool, error) {
	if !validOwner(tenant, user, sid) {
		return true, errors.New("session ownership is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var state struct{ Revoked bool }
	err := q.Raw(`SELECT
		NOT EXISTS (SELECT 1 FROM sys_refresh_token
			WHERE tenant_id = ? AND user_id = ? AND session_id = ? AND revoked_at IS NULL)
		OR EXISTS (SELECT 1 FROM sys_refresh_token
			WHERE tenant_id = ? AND user_id = ? AND session_id = ?
			AND revoked_reason IN ('logout', 'kick', 'replay')) AS revoked`,
		tenant, user, sid, tenant, user, sid).Find(&state).Error
	return state.Revoked, err
}
