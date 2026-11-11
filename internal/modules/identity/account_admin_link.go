package identity

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
)

// ResetLinkView exposes a one-time credential to an authorized administrator only.
type ResetLinkView struct {
	Link string `json:"link"`
}

// CreateResetLink checks management scope and audits only safe token metadata.
func (s *AccountService) CreateResetLink(ctx context.Context, actor Overview, id int64, meta RequestMeta) (ResetLinkView, error) {
	raw, err := refresh.Generate()
	if err != nil {
		return ResetLinkView{}, accountError(err)
	}
	link := AccountLink(s.PublicURL, "reset", actor.User.TenantID, raw)
	err = s.inviteTransaction(ctx, actor, func(tx context.Context) error {
		user, err := s.Admin.store.ReadUser(tx, actor.User.TenantID, id)
		if err != nil {
			return err
		}
		if user.IsSuper && !actor.User.IsSuper {
			return denied()
		}
		if err := s.Store.LockAccount(tx, user.TenantID, user.ID); err != nil {
			return err
		}
		now := s.Now().UTC()
		row := model.AuthToken{TenantID: user.TenantID, UserID: &user.ID, Purpose: "reset", TokenHash: refresh.Hash(raw), ExpiresAt: now.Add(30 * time.Minute),
			CreatedBy: actor.User.ID, CreatedAt: now, MaxUses: 1, RoleIDs: []byte(`[]`)}
		if err := s.Store.InsertToken(tx, &row); err != nil {
			return err
		}
		if s.Notify != nil {
			if _, err := s.Notify.Queue(tx, row.TenantID, user.ID, "reset", row.ID, link, meta.RequestID); err != nil {
				return err
			}
		}
		return s.Admin.record(tx, actor, "identity.password.reset_link", "user", user.ID, nil, map[string]any{"token_id": row.ID}, false)
	})
	if err != nil {
		return ResetLinkView{}, accountError(err)
	}
	return ResetLinkView{Link: link}, nil
}

func (h accountHandler) resetLink(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, err := adminID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	out, err := h.service.CreateResetLink(c.Request.Context(), AdminActor(c), id, requestMeta(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, out)
}
