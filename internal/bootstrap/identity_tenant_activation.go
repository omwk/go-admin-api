package bootstrap

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

func (s *accountStore) ActivateInvitedUser(ctx context.Context, row model.AuthToken, in identity.AcceptInviteInput, hash string, now time.Time) (model.User, error) {
	invalid := errs.New(errs.ErrInvalidParam, 400, "邀请或链接无效")
	if row.UserID == nil || row.Username == nil || in.Username != "" && in.Username != *row.Username || in.Phone != nil {
		return model.User{}, invalid
	}
	if err := s.LockAccount(ctx, row.TenantID, *row.UserID); err != nil {
		return model.User{}, err
	}
	user, found, err := s.UserForReauth(ctx, row.TenantID, *row.UserID)
	if err != nil {
		return user, err
	}
	if !found || user.Username != *row.Username || user.Status != "disabled" || user.PasswordHash != "" || !user.IsSuper {
		return user, invalid
	}
	if in.Email != nil && (user.Email == nil || *in.Email != *user.Email) {
		return user, invalid
	}
	q, cancel := s.query(ctx)
	defer cancel()
	result := q.Model(&model.User{}).Where("tenant_id=? AND id=? AND status='disabled' AND password_hash=''", row.TenantID, user.ID).
		Updates(map[string]any{"password_hash": hash, "password_changed_at": now, "force_password_change": false, "status": "active"})
	if err := adminMutation(result); err != nil {
		return user, err
	}
	user.PasswordHash, user.PasswordChangedAt, user.Status = hash, &now, "active"
	return user, nil
}
