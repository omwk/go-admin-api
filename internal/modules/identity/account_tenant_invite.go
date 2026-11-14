package identity

import (
	"context"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

type invitedActivation interface {
	ActivateInvitedUser(context.Context, model.AuthToken, AcceptInviteInput, string, time.Time) (model.User, error)
}

func (s *AccountService) acceptInvitedUser(ctx context.Context, row model.AuthToken, in AcceptInviteInput, hash string, now time.Time, roles []int64) (model.User, error) {
	if row.UserID != nil {
		store, ok := s.Store.(invitedActivation)
		if !ok {
			return model.User{}, invalidLink()
		}
		return store.ActivateInvitedUser(ctx, row, in, hash, now)
	}
	user, err := invitedUser(row, in, hash)
	if err != nil {
		return user, err
	}
	user.PasswordChangedAt = &now
	active, err := s.Store.HasActiveTenantAdmin(ctx, row.TenantID)
	if err != nil {
		return user, err
	}
	user.IsSuper = !active
	if err := s.Store.CreateInvitedUser(ctx, &user, roles); err != nil {
		return user, invalidLink()
	}
	return user, nil
}
