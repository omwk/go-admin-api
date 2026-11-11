package shop

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

// CredentialVersions reads retired version metadata under the current shop scope.
func (s *Service) CredentialVersions(ctx context.Context, actor Actor, id int64) ([]repo.CredentialVersionView, error) {
	ctx, err := s.authorize(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return nil, err
	}
	store, ok := s.store.(repo.CredentialStore)
	if !ok {
		return nil, errs.New(errs.ErrInternal, 500, "凭据存储不可用")
	}
	rows, err := store.CredentialVersions(ctx, actor.TenantID, id)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].IssuedAt = rows[i].IssuedAt.UTC()
		rows[i].ExpiresAt, rows[i].RetiredAt, rows[i].PurgeAfter = utcTime(rows[i].ExpiresAt), utcTime(rows[i].RetiredAt), utcTime(rows[i].PurgeAfter)
	}
	return rows, nil
}

func (h shopHandler) credentialVersions(c *gin.Context) {
	id, err := credentialID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	rows, err := h.service.CredentialVersions(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, rows)
}
