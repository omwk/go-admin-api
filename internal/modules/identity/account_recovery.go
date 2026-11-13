package identity

import (
	"context"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

// RecoveryResult has the same shape for existing and nonexistent accounts.
type RecoveryResult struct {
	Accepted bool   `json:"accepted"`
	Link     string `json:"dev_reset_url,omitempty"`
}

// ForgotPassword always accepts an unknown account and queues known accounts transactionally.
func (s *AccountService) ForgotPassword(ctx context.Context, tenant int64, account string, meta RequestMeta) (RecoveryResult, error) {
	if err := s.allowAccount(ctx, "forgot-ip", meta.IP); err != nil {
		return RecoveryResult{}, err
	}
	_, normalized, normalizeErr := AccountIdentifier(account)
	if normalizeErr != nil {
		normalized = strings.ToLower(strings.TrimSpace(account))
	}
	if err := s.allowAccount(ctx, "forgot-account", accountBudget(tenant, normalized)); err != nil {
		return RecoveryResult{}, err
	}
	defer recoveryResponseFloor(ctx, time.Now())
	raw, err := refresh.Generate()
	if err != nil {
		return RecoveryResult{}, accountError(err)
	}
	out := RecoveryResult{Accepted: true}
	if s.CopyResetLinks {
		out.Link = AccountLink(s.PublicURL, "reset", tenant, raw)
	}
	user, found, err := s.Store.UserForLogin(ctx, tenant, account)
	if err != nil {
		return RecoveryResult{}, accountError(err)
	}
	if !found || user.Status == "disabled" {
		return out, nil
	}
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, tenant, user.ID); err != nil {
			return err
		}
		current, found, err := s.Store.UserForReauth(tx, tenant, user.ID)
		if err != nil {
			return err
		}
		if !found || current.Status == "disabled" {
			return nil
		}
		now := s.Now().UTC()
		row := model.AuthToken{TenantID: tenant, UserID: &user.ID, Purpose: "reset", TokenHash: refresh.Hash(raw), ExpiresAt: now.Add(30 * time.Minute), CreatedBy: user.ID, CreatedAt: now, MaxUses: 1, RoleIDs: []byte(`[]`)}
		if err := s.Store.InsertToken(tx, &row); err != nil {
			return err
		}
		if s.Notify != nil {
			_, err = s.Notify.Queue(tx, tenant, user.ID, "reset", row.ID, AccountLink(s.PublicURL, "reset", tenant, raw), meta.RequestID)
		}
		return err
	})
	if err != nil {
		return RecoveryResult{}, accountError(err)
	}
	return out, nil
}

func recoveryResponseFloor(ctx context.Context, started time.Time) {
	remaining := 250*time.Millisecond - time.Since(started)
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// ResetPassword consumes one valid reset and revokes every prior session and reset token.
func (s *AccountService) ResetPassword(ctx context.Context, tenant int64, raw, password string, meta RequestMeta) error {
	if err := s.allowAccount(ctx, "reset-ip", meta.IP); err != nil {
		return err
	}
	if err := validatePassword(ctx, s.Settings, tenant, password); err != nil {
		return err
	}
	digest := refresh.Hash(raw)
	candidate, found, err := s.Store.TokenByHash(ctx, tenant, "reset", digest)
	if err != nil {
		return accountError(err)
	}
	if !found || candidate.UserID == nil || !candidate.Valid(s.Now().UTC()) {
		return invalidLink()
	}
	hash, err := pwd.Hash(password)
	if err != nil {
		return accountError(err)
	}
	var marks []revocation
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, tenant, *candidate.UserID); err != nil {
			return err
		}
		row, found, err := s.Store.TokenByHash(tx, tenant, "reset", digest)
		if err != nil {
			return err
		}
		if !found || !row.Valid(s.Now().UTC()) || row.UserID == nil {
			return invalidLink()
		}
		user, found, err := s.Store.UserForReauth(tx, tenant, *row.UserID)
		if err != nil {
			return err
		}
		if !found || user.Status == "disabled" {
			return invalidLink()
		}
		if err := s.writePassword(tx, &user, password, hash, false); err != nil {
			return err
		}
		now := s.Now().UTC()
		row.UsedAt, row.UsedCount = &now, 1
		if err := s.Store.SaveToken(tx, row); err != nil {
			return err
		}
		marks, err = s.revokeSessions(tx, user, "")
		return err
	})
	if err != nil {
		return accountError(err)
	}
	return s.Auth.mark(ctx, marks)
}
