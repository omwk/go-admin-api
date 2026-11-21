package identity

import (
	"context"
	"strconv"
	"strings"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
)

// ProfileInput contains the scalar fields an account may update itself.
type ProfileInput struct {
	Nickname string  `json:"nickname" binding:"max=256"`
	Email    *string `json:"email" binding:"omitempty,max=256"`
	Phone    *string `json:"phone" binding:"omitempty,max=32"`
	Avatar   *string `json:"avatar" binding:"omitempty,max=2048"`
}

// SetAvatar changes only the verified account's avatar within the caller's file transaction.
func (s *AccountService) SetAvatar(ctx context.Context, tenant, userID int64, path string) error {
	if path != "/api/v1/avatars/"+strconv.FormatInt(userID, 10) {
		return invalidAdmin()
	}
	claims := jwt.Claims{TenantID: tenant, Subject: strconv.FormatInt(userID, 10)}
	return s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, tenant, userID); err != nil {
			return err
		}
		user, err := s.owner(tx, claims)
		if err != nil {
			return err
		}
		before := userAuditState(user)
		user.Avatar = &path
		if err := s.Store.UpdateProfile(tx, user); err != nil {
			return accountError(err)
		}
		before, after, changed := audit.Changes(before, userAuditState(user))
		if !changed {
			return nil
		}
		return s.Auth.audit(tx, audit.Event{TenantID: tenant, ActorType: "user", ActorID: &userID, ActorName: user.Username, ResourceLabel: user.Username, Action: "system.user.profile", ResourceType: "user", ResourceID: claims.Subject, Before: before, After: after, Result: "success"})
	})
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
		if in.Email != nil && !sameEmail(user.Email, profileContact(*in.Email)) {
			return errs.New(errs.ErrInvalidParam, 400, "请通过邮箱验证流程变更邮箱")
		}
		before := userAuditState(user)
		before["phone"] = user.Phone
		user.Nickname = &in.Nickname
		if in.Avatar != nil {
			user.Avatar = in.Avatar
		}
		if in.Phone != nil {
			user.Phone = profileContact(*in.Phone)
		}
		if err := s.Store.UpdateProfile(tx, user); err != nil {
			return err
		}
		after := userAuditState(user)
		after["phone"] = user.Phone
		before, after, changed := audit.Changes(before, after)
		if !changed {
			return nil
		}
		return s.Auth.audit(tx, audit.Event{
			TenantID: user.TenantID, ActorType: "user", ActorID: &user.ID,
			ActorName: user.Username, ResourceLabel: user.Username,
			Action: "system.user.profile", ResourceType: "user", ResourceID: strconv.FormatInt(user.ID, 10),
			Before: before, After: after, Result: "success",
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
