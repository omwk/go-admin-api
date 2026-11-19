package identity

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// EmailChangeVerified is the account's newly verified login alias.
type EmailChangeVerified struct {
	Email string `json:"email"`
}

// VerifyEmailChange consumes one capability and updates its owner in the same transaction.
func (s *AccountService) VerifyEmailChange(ctx context.Context, raw string, meta RequestMeta) (EmailChangeVerified, bool, error) {
	if raw == "" || len(raw) > 128 {
		return EmailChangeVerified{}, false, invalidLink()
	}
	if err := s.allowAccount(ctx, "email-verify-ip", meta.IP); err != nil {
		return EmailChangeVerified{}, false, err
	}
	store, ok := s.Store.(repo.EmailChange)
	if !ok {
		return EmailChangeVerified{}, false, dependencyFailure(errors.New("email change unavailable"))
	}
	candidate, found, err := store.EmailChangeByHash(ctx, refresh.Hash(raw))
	if err != nil {
		return EmailChangeVerified{}, false, accountError(err)
	}
	if !found || candidate.UserID == nil || !candidate.Valid(s.Now().UTC()) {
		return EmailChangeVerified{}, false, invalidLink()
	}
	var email string
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if gate, ok := s.Store.(repo.TenantGate); ok {
			if err := gate.CheckTenant(tx, candidate.TenantID); err != nil {
				return err
			}
		}
		if err := s.Store.LockAccount(tx, candidate.TenantID, *candidate.UserID); err != nil {
			return err
		}
		row, found, err := s.Store.TokenByHash(tx, candidate.TenantID, "email_change", candidate.TokenHash)
		if err != nil {
			return err
		}
		if !found || row.UserID == nil || row.NewEmail == nil || !row.Valid(s.Now().UTC()) {
			return invalidLink()
		}
		email, err = s.applyEmailChange(tx, row, meta)
		return err
	})
	if err != nil {
		return EmailChangeVerified{}, false, accountError(err)
	}
	pending := false
	if s.Invalidate != nil {
		pending = s.Invalidate(ctx, candidate.TenantID, []int64{*candidate.UserID})
	}
	return EmailChangeVerified{Email: email}, pending, nil
}

func (s *AccountService) applyEmailChange(ctx context.Context, row model.AuthToken, meta RequestMeta) (string, error) {
	user, found, err := s.Store.UserForReauth(ctx, row.TenantID, *row.UserID)
	if err != nil {
		return "", err
	}
	if !found || user.Status != "active" || !sameEmail(user.Email, row.OriginalEmail) {
		return "", invalidLink()
	}
	if err := s.emailAvailable(ctx, user.TenantID, *row.NewEmail); err != nil {
		return "", err
	}
	user.Email = row.NewEmail
	if err := s.Store.UpdateProfile(ctx, user); err != nil {
		return "", err
	}
	now := s.Now().UTC()
	row.UsedAt, row.UsedCount = &now, 1
	if err := s.Store.SaveToken(ctx, row); err != nil {
		return "", err
	}
	if err := s.emailChangeAudit(ctx, user, "identity.email.change.verify", meta); err != nil {
		return "", err
	}
	return *row.NewEmail, nil
}
