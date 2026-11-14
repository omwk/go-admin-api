package identity

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func (s *AccountService) queueVerification(ctx context.Context, store repo.Registration, user model.User, raw string, now time.Time, meta RequestMeta) error {
	if err := store.RevokeVerificationTokens(ctx, user.TenantID, user.ID, now); err != nil {
		return err
	}
	row := model.AuthToken{TenantID: user.TenantID, UserID: &user.ID, Purpose: "verify", TokenHash: refresh.Hash(raw),
		ExpiresAt: now.Add(24 * time.Hour), CreatedBy: user.ID, CreatedAt: now, MaxUses: 1, RoleIDs: []byte(`[]`)}
	if err := s.Store.InsertToken(ctx, &row); err != nil {
		return err
	}
	if s.Notify == nil {
		return errors.New("registration notifications unavailable")
	}
	_, err := s.Notify.Queue(ctx, user.TenantID, user.ID, "verify", row.ID,
		AccountLink(s.PublicURL, "verify", user.TenantID, raw), meta.RequestID)
	return err
}

func (s *AccountService) registrationMailAvailable(ctx context.Context, tenant int64) (bool, error) {
	if s.CopyResetLinks {
		return true, nil
	}
	notifier, ok := s.Notify.(interface {
		EmailAvailable(context.Context, int64) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return notifier.EmailAvailable(ctx, tenant)
}

// VerifyRegistration activates one pending user and elects the first active super under the invite lock.
func (s *AccountService) VerifyRegistration(ctx context.Context, raw string, meta RequestMeta) error {
	if raw == "" || len(raw) > 128 {
		return invalidLink()
	}
	if err := s.allowAccount(ctx, "verify-ip", meta.IP); err != nil {
		return err
	}
	store, ok := s.Store.(repo.Registration)
	if !ok {
		return dependencyFailure(errors.New("registration store unavailable"))
	}
	candidate, found, err := store.VerificationByHash(ctx, refresh.Hash(raw))
	if err != nil {
		return accountError(err)
	}
	if !found || candidate.UserID == nil || !candidate.Valid(s.Now().UTC()) {
		return invalidLink()
	}
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockInviteTenant(tx, candidate.TenantID); err != nil {
			return err
		}
		if gate, ok := s.Store.(repo.TenantGate); ok {
			if err := gate.CheckTenant(tx, candidate.TenantID); err != nil {
				return err
			}
		}
		if err := s.Store.LockAccount(tx, candidate.TenantID, *candidate.UserID); err != nil {
			return err
		}
		row, found, err := s.Store.TokenByHash(tx, candidate.TenantID, "verify", candidate.TokenHash)
		if err != nil {
			return err
		}
		if !found || row.UserID == nil || !row.Valid(s.Now().UTC()) {
			return invalidLink()
		}
		return s.activateRegistration(tx, store, row, meta)
	})
	return accountError(err)
}

func (s *AccountService) activateRegistration(ctx context.Context, store repo.Registration, row model.AuthToken, meta RequestMeta) error {
	user, found, err := s.Store.UserForReauth(ctx, row.TenantID, *row.UserID)
	if err != nil {
		return err
	}
	if !found || user.Status != "disabled" {
		return invalidLink()
	}
	active, err := s.Store.HasActiveTenantAdmin(ctx, row.TenantID)
	if err != nil {
		return err
	}
	user.IsSuper, user.Status = !active, "active"
	roles := []int64{}
	if active {
		policy, err := setting.LoadTenantRegistration(ctx, s.Settings, row.TenantID)
		if err != nil {
			return err
		}
		roles, err = store.RegistrationRoles(ctx, row.TenantID, policy.DefaultRoleCode)
		if err != nil {
			return err
		}
	}
	if err := store.ActivateRegisteredUser(ctx, user, roles); err != nil {
		return err
	}
	now := s.Now().UTC()
	row.UsedAt, row.UsedCount = &now, 1
	if err := s.Store.SaveToken(ctx, row); err != nil {
		return err
	}
	if err := store.RevokeVerificationTokens(ctx, row.TenantID, user.ID, now); err != nil {
		return err
	}
	return s.registrationAudit(ctx, user, "identity.register.verify", meta)
}

func (s *AccountService) registrationAudit(ctx context.Context, user model.User, action string, meta RequestMeta) error {
	if s.Auth == nil || s.Auth.audit == nil {
		return errors.New("registration audit unavailable")
	}
	meta = boundedRequestMeta(meta)
	return s.Auth.audit(ctx, audit.Event{TenantID: user.TenantID, ActorType: "user", ActorID: &user.ID, Action: action,
		ResourceType: "user", ResourceID: strconv.FormatInt(user.ID, 10), Result: "success", IP: meta.IP, UserAgent: meta.UserAgent,
		After: map[string]any{"status": user.Status, "is_super": user.IsSuper}})
}
