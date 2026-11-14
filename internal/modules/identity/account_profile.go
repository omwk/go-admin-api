package identity

import (
	"context"
	"strconv"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
)

// ProfileInput contains the scalar fields an account may update itself.
type ProfileInput struct {
	Nickname string  `json:"nickname" binding:"max=256"`
	Email    *string `json:"email" binding:"omitempty,max=256"`
	Phone    *string `json:"phone" binding:"omitempty,max=32"`
	Avatar   *string `json:"avatar" binding:"omitempty,max=2048"`
}

// UpdateProfile updates only the caller's profile and invalidates profile projections.
func (s *AccountService) UpdateProfile(ctx context.Context, claims jwt.Claims, in ProfileInput) (AdminUser, bool, error) {
	user, err := s.owner(ctx, claims)
	if err != nil {
		return AdminUser{}, false, err
	}
	if err := normalizeProfileContacts(&in); err != nil {
		return AdminUser{}, false, err
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
		before := map[string]any{"phone": user.Phone}
		user.Nickname = &in.Nickname
		if in.Email != nil {
			user.Email = profileContact(*in.Email)
		}
		if in.Avatar != nil {
			user.Avatar = in.Avatar
		}
		if in.Phone != nil {
			user.Phone = profileContact(*in.Phone)
		}
		if err := s.Store.UpdateProfile(tx, user); err != nil {
			return err
		}
		return s.Auth.audit(tx, audit.Event{
			TenantID: user.TenantID, ActorType: "user", ActorID: &user.ID,
			Action: "system:user:profile", ResourceType: "user", ResourceID: strconv.FormatInt(user.ID, 10),
			Before: before, After: map[string]any{"phone": user.Phone}, Result: "success",
		})
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

func normalizeProfileContacts(in *ProfileInput) error {
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if email != "" {
			column, normalized, err := AccountIdentifier(email)
			if err != nil || column != "email" {
				return invalidAdmin()
			}
			in.Email = &normalized
		}
	}
	if in.Phone != nil && *in.Phone != "" {
		phone, err := NormalizePhone(*in.Phone)
		if err != nil {
			return invalidAdmin()
		}
		in.Phone = &phone
	}
	return nil
}

func profileContact(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
