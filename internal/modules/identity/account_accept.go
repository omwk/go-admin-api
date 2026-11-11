package identity

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

// AcceptInviteInput supplies only identity identifiers and the invitee's password.
type AcceptInviteInput struct {
	Token    string  `json:"token" binding:"required,max=128"`
	Username string  `json:"username" binding:"max=256"`
	Email    *string `json:"email" binding:"omitempty,max=256"`
	Phone    *string `json:"phone" binding:"omitempty,max=32"`
	Password string  `json:"password" binding:"required,max=72"`
}

// AcceptInvite atomically consumes one use and provisions the preset user and roles.
func (s *AccountService) AcceptInvite(ctx context.Context, tenant int64, in AcceptInviteInput, meta RequestMeta) (UserSummary, error) {
	if err := s.allowAccount(ctx, "invite-ip", meta.IP); err != nil {
		return UserSummary{}, err
	}
	if pwd.Validate(in.Password) != nil {
		return UserSummary{}, invalidPassword()
	}
	hash, err := pwd.Hash(in.Password)
	if err != nil {
		return UserSummary{}, accountError(err)
	}
	var user model.User
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		row, found, err := s.Store.TokenByHash(tx, tenant, "invite", refresh.Hash(in.Token))
		if err != nil {
			return err
		}
		now := s.Now().UTC()
		if !found || !row.Valid(now) || row.DepartmentID == nil {
			return invalidLink()
		}
		user, err = invitedUser(row, in, hash)
		if err != nil {
			return err
		}
		user.PasswordChangedAt = &now
		var roles []int64
		if err := json.Unmarshal(row.RoleIDs, &roles); err != nil {
			return err
		}
		if err := s.Store.CreateInvitedUser(tx, &user, roles); err != nil {
			return invalidLink()
		}
		if err := rememberInitialPassword(tx, s, user, now); err != nil {
			return err
		}
		row.UsedCount++
		if row.MaxUses > 0 && row.UsedCount >= row.MaxUses {
			row.UsedAt = &now
		}
		return s.Store.SaveToken(tx, row)
	})
	if err != nil {
		return UserSummary{}, accountError(err)
	}
	return UserSummary{ID: user.ID, Username: user.Username, Nickname: user.Nickname}, nil
}

func invitedUser(row model.AuthToken, in AcceptInviteInput, hash string) (model.User, error) {
	name := in.Username
	if row.Username != nil {
		if name != "" && name != *row.Username {
			return model.User{}, invalidLink()
		}
		name = *row.Username
	}
	user := model.User{TenantID: row.TenantID, DepartmentID: *row.DepartmentID, PasswordHash: hash, Status: "active"}
	if in.Email != nil {
		column, value, err := AccountIdentifier(*in.Email)
		if err != nil || column != "email" {
			return user, invalidAdmin()
		}
		user.Email = &value
		if name == "" {
			name = value
		}
	}
	if in.Phone != nil {
		value, err := NormalizePhone(*in.Phone)
		if err != nil {
			return user, invalidAdmin()
		}
		user.Phone = &value
		if name == "" {
			name = value
		}
	}
	column, value, err := AccountIdentifier(name)
	if err != nil {
		return user, invalidAdmin()
	}
	user.Username = value
	if column == "email" && user.Email == nil {
		user.Email = &value
	}
	if column == "phone" && user.Phone == nil {
		user.Phone = &value
	}
	return user, nil
}

func (s *AccountService) allowAccount(ctx context.Context, purpose, value string) error {
	if s.Limits == nil {
		return dependencyFailure(errors.New("account limits unavailable"))
	}
	allowed, err := s.Limits.AllowAccount(ctx, purpose, value)
	if err != nil {
		return dependencyFailure(err)
	}
	if !allowed {
		return errs.New(errs.ErrTooManyRequests, 429, "请求过多，请稍后重试")
	}
	return nil
}
