package identity

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// EmailChangeNotifications queues confirmation and notice together without external I/O.
type EmailChangeNotifications interface {
	QueueEmailChange(context.Context, model.AuthToken, string, RequestMeta) error
}

// EmailChangePending acknowledges a queued, unverified request without disclosing its token.
type EmailChangePending struct {
	Pending        bool `json:"pending"`
	ExpiresInHours int  `json:"expires_in_hours"`
}

// ChangeEmail keeps the current login alias until the new mailbox proves ownership.
func (s *AccountService) ChangeEmail(ctx context.Context, claims jwt.Claims, email string, meta RequestMeta) (EmailChangePending, error) {
	column, email, err := AccountIdentifier(email)
	if err != nil || column != "email" {
		return EmailChangePending{}, invalidAdmin()
	}
	user, err := s.owner(ctx, claims)
	if err != nil {
		return EmailChangePending{}, err
	}
	if err := s.allowAccount(ctx, "email-change-user", accountBudget(user.TenantID, strconv.FormatInt(user.ID, 10))); err != nil {
		return EmailChangePending{}, err
	}
	store, ok := s.Store.(repo.EmailChange)
	notifier, canNotify := s.Notify.(EmailChangeNotifications)
	if !ok || !canNotify {
		return EmailChangePending{}, dependencyFailure(errors.New("email change unavailable"))
	}
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, user.TenantID, user.ID); err != nil {
			return err
		}
		user, err := s.owner(tx, claims)
		if err != nil {
			return err
		}
		if sameEmail(user.Email, &email) {
			return invalidAdmin()
		}
		if err := s.emailAvailable(tx, user.TenantID, email); err != nil {
			return err
		}
		raw, err := refresh.Generate()
		if err != nil {
			return err
		}
		link, err := s.accountLink(tx, "email_change", user.TenantID, raw)
		if err != nil {
			return err
		}
		now := s.Now().UTC()
		if err := store.RevokeEmailChangeTokens(tx, user.TenantID, user.ID, now); err != nil {
			return err
		}
		row := model.AuthToken{TenantID: user.TenantID, UserID: &user.ID, Purpose: "email_change", TokenHash: refresh.Hash(raw),
			CreatedAt: now, CreatedBy: user.ID, ExpiresAt: now.Add(24 * time.Hour), MaxUses: 1, RoleIDs: []byte(`[]`), OriginalEmail: user.Email, NewEmail: &email}
		if err := s.Store.InsertToken(tx, &row); err != nil {
			return err
		}
		if err := notifier.QueueEmailChange(tx, row, link, boundedRequestMeta(meta)); err != nil {
			return err
		}
		return s.emailChangeAudit(tx, user, "identity.email.change.request", meta)
	})
	return EmailChangePending{Pending: err == nil, ExpiresInHours: 24}, accountError(err)
}

func sameEmail(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return strings.EqualFold(strings.TrimSpace(*left), strings.TrimSpace(*right))
}

func (s *AccountService) emailAvailable(ctx context.Context, tenant int64, email string) error {
	_, found, err := s.Store.UserForLogin(ctx, tenant, email)
	if err != nil {
		return err
	}
	if found {
		return errs.New(errs.ErrConflict, 409, "该邮箱已被使用")
	}
	return nil
}

func (s *AccountService) emailChangeAudit(ctx context.Context, user model.User, action string, meta RequestMeta) error {
	if s.Auth == nil || s.Auth.audit == nil {
		return errors.New("email change audit unavailable")
	}
	meta = boundedRequestMeta(meta)
	return s.Auth.audit(ctx, audit.Event{TenantID: user.TenantID, ActorType: "user", ActorID: &user.ID,
		Action: action, ResourceType: "user", ResourceID: strconv.FormatInt(user.ID, 10), Result: "success",
		IP: meta.IP, UserAgent: meta.UserAgent, After: map[string]any{"email": "***"}})
}
