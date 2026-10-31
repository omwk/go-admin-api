package identity

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// UseSessions wires management reads to the same authentication service and DB session lock.
func (s *AdminService) UseSessions(store repo.Sessions, auth *AuthService) {
	s.sessions = store
	s.auth = auth
}

// Sessions returns only families with a currently valid, non-revoked refresh.
func (s *AdminService) Sessions(ctx context.Context, actor Overview, page repo.Page) (AdminPage[repo.Session], error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return AdminPage[repo.Session]{}, err
	}
	rows, total, err := s.sessions.Sessions(ctx, actor.User.TenantID, page, s.auth.now().UTC())
	return AdminPage[repo.Session]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, err
}

// Kick revokes a known family under the refresh lock, commits, then writes Redis markers.
func (s *AdminService) Kick(ctx context.Context, actor Overview, sid string) error {
	if sid == "" || len(sid) > 128 {
		return invalidAdmin()
	}
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return err
	}
	var marks []revocation
	err = s.store.Transaction(ctx, func(ctx context.Context) error {
		if err := s.auth.store.LockSession(ctx, actor.User.TenantID, sid); err != nil {
			return err
		}
		family, err := s.sessions.SessionFamily(ctx, actor.User.TenantID, sid)
		if err != nil {
			return err
		}
		owner := family.Tokens[0].UserID
		if err := s.auth.store.RevokeFamily(ctx, actor.User.TenantID, owner, sid, "kick", s.auth.now().UTC()); err != nil {
			return err
		}
		marks = familyMarks(family, sid)
		return s.audit(ctx, audit.Event{TenantID: actor.User.TenantID, ActorType: "user", ActorID: &actor.User.ID, Action: permission.SystemSessionKick, ResourceType: "session", ResourceID: sid, After: map[string]any{"user_id": owner, "revoked": true}, Result: "success"})
	})
	if err != nil {
		return err
	}
	return s.auth.mark(ctx, marks)
}

func (h adminHandler) sessions(c *gin.Context) {
	page, err := adminPagination(c, false)
	if err == nil && page.Sort != "-created_at" {
		err = invalidAdmin()
	}
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	result, err := h.service.Sessions(c.Request.Context(), adminActor(c), page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h adminHandler) kick(c *gin.Context) {
	if err := h.service.Kick(c.Request.Context(), adminActor(c), c.Param("sid")); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}
