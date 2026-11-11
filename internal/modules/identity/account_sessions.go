package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/jwt"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// MySessions lists only the current user's unexpired session families.
func (s *AccountService) MySessions(ctx context.Context, claims jwt.Claims, page repo.Page) (AdminPage[repo.Session], error) {
	user, err := s.owner(ctx, claims)
	if err != nil {
		return AdminPage[repo.Session]{}, err
	}
	rows, total, err := s.Store.OwnSessions(ctx, user.TenantID, user.ID, page, s.Now().UTC())
	return AdminPage[repo.Session]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, accountError(err)
}

// DeleteSession persists and marks revocation only after proving family ownership.
func (s *AccountService) DeleteSession(ctx context.Context, claims jwt.Claims, sid string) error {
	if sid == "" || len(sid) > 128 {
		return invalidAdmin()
	}
	user, err := s.owner(ctx, claims)
	if err != nil {
		return err
	}
	var marks []revocation
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, user.TenantID, user.ID); err != nil {
			return err
		}
		if err := s.Auth.store.LockSession(tx, user.TenantID, sid); err != nil {
			return err
		}
		family, err := s.Auth.store.Family(tx, user.TenantID, user.ID, sid)
		if err != nil {
			return err
		}
		if len(family.Tokens) == 0 {
			return adminNotFound()
		}
		if err := s.Auth.store.RevokeFamily(tx, user.TenantID, user.ID, sid, "logout", s.Now().UTC()); err != nil {
			return err
		}
		marks = familyMarks(family, sid)
		return nil
	})
	if err != nil {
		return accountError(err)
	}
	return s.Auth.mark(ctx, marks)
}

// LogoutAll revokes every session, including the calling session.
func (s *AccountService) LogoutAll(ctx context.Context, claims jwt.Claims) error {
	user, err := s.owner(ctx, claims)
	if err != nil {
		return err
	}
	var marks []revocation
	err = s.Store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Store.LockAccount(tx, user.TenantID, user.ID); err != nil {
			return err
		}
		var err error
		marks, err = s.revokeSessions(tx, user, "")
		return err
	})
	if err != nil {
		return accountError(err)
	}
	return s.Auth.mark(ctx, marks)
}

func (s *AccountService) revokeSessions(ctx context.Context, user model.User, keep string) ([]revocation, error) {
	ids, err := s.Store.SessionIDs(ctx, user.TenantID, user.ID)
	if err != nil {
		return nil, err
	}
	var marks []revocation
	for _, sid := range ids {
		if sid == keep {
			continue
		}
		if err := s.Auth.store.LockSession(ctx, user.TenantID, sid); err != nil {
			return nil, err
		}
		family, err := s.Auth.store.Family(ctx, user.TenantID, user.ID, sid)
		if err != nil {
			return nil, err
		}
		if err := s.Auth.store.RevokeFamily(ctx, user.TenantID, user.ID, sid, "logout", s.Now().UTC()); err != nil {
			return nil, err
		}
		marks = append(marks, familyMarks(family, sid)...)
	}
	return marks, nil
}
