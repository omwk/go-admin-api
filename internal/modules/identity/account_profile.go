package identity

import (
	"context"
	"strings"

	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
)

// ProfileInput contains the scalar fields an account may update itself.
type ProfileInput struct {
	Nickname string  `json:"nickname" binding:"max=256"`
	Email    *string `json:"email" binding:"omitempty,max=256"`
	Avatar   *string `json:"avatar" binding:"omitempty,max=2048"`
}

// UpdateProfile updates only the caller's profile and invalidates profile projections.
func (s *AccountService) UpdateProfile(ctx context.Context, claims jwt.Claims, in ProfileInput) (AdminUser, bool, error) {
	user, err := s.owner(ctx, claims)
	if err != nil {
		return AdminUser{}, false, err
	}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if email != "" {
			column, normalized, err := AccountIdentifier(email)
			if err != nil || column != "email" {
				return AdminUser{}, false, invalidAdmin()
			}
			in.Email = &normalized
		}
	}
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, user.TenantID, user.ID); err != nil {
			return err
		}
		var err error
		user, err = s.owner(tx, claims)
		if err != nil {
			return err
		}
		user.Nickname = &in.Nickname
		if in.Email != nil {
			user.Email = in.Email
			if *in.Email == "" {
				user.Email = nil
			}
		}
		if in.Avatar != nil {
			user.Avatar = in.Avatar
		}
		return s.Store.UpdateProfile(tx, user)
	})
	if err != nil {
		return AdminUser{}, false, accountError(err)
	}
	pending := false
	if s.Invalidate != nil {
		pending = s.Invalidate(ctx, user.TenantID, []int64{user.ID})
	}
	return adminUser(user), pending, nil
}
