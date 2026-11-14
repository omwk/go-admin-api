package identity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// TenantAdminInviteResult exposes the invitation only to the issuing platform administrator.
type TenantAdminInviteResult struct {
	InviteURL string    `json:"invite_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ReissueAdminInvite runs within the management transaction, locking before inspecting users or tokens.
func (s *TenantService) ReissueAdminInvite(ctx context.Context, actor Overview, id int64, force bool) (TenantAdminInviteResult, error) {
	if err := s.global(actor); err != nil {
		return TenantAdminInviteResult{}, err
	}
	if err := s.store.LockInviteTenant(ctx, id); err != nil {
		return TenantAdminInviteResult{}, err
	}
	tenant, err := s.store.Tenant(ctx, id, true)
	if err != nil {
		return TenantAdminInviteResult{}, err
	}
	if tenant.Status != "active" {
		return TenantAdminInviteResult{}, errs.New(errs.ErrConflict, 409, "租户已停用")
	}
	active, err := s.store.HasActiveTenantAdmin(ctx, id)
	if err != nil {
		return TenantAdminInviteResult{}, err
	}
	if active && !force {
		return TenantAdminInviteResult{}, errs.New(errs.ErrConflict, 409, "该租户已有管理员")
	}
	token, err := s.store.TenantInviteTarget(ctx, id)
	if err != nil {
		return TenantAdminInviteResult{}, err
	}
	raw, err := refresh.Generate()
	if err != nil {
		return TenantAdminInviteResult{}, err
	}
	now := s.now().UTC()
	token.Purpose, token.TokenHash, token.MaxUses = "invite", refresh.Hash(raw), 1
	token.RoleIDs = json.RawMessage(`[]`)
	token.CreatedAt, token.ExpiresAt = now, now.AddDate(0, 0, 7)
	if err := s.store.ReplaceTenantInvite(ctx, &token, now); err != nil {
		return TenantAdminInviteResult{}, err
	}
	if err := s.record(ctx, actor, "system:tenant:admin_invite", "tenant", id, id, nil, map[string]any{"force": force}, false); err != nil {
		return TenantAdminInviteResult{}, err
	}
	return TenantAdminInviteResult{InviteURL: AccountLink(s.publicURL, "invite", id, raw), ExpiresAt: token.ExpiresAt}, nil
}

// TenantMembers permits only the configured platform super administrator to inspect membership.
func (s *TenantService) TenantMembers(ctx context.Context, actor Overview, id int64, page repo.Page) (AdminPage[model.TenantMember], error) {
	if err := s.global(actor); err != nil {
		return AdminPage[model.TenantMember]{}, err
	}
	if _, err := s.store.Tenant(ctx, id, false); err != nil {
		return AdminPage[model.TenantMember]{}, err
	}
	rows, total, err := s.store.TenantMembers(ctx, id, page)
	if err != nil {
		return AdminPage[model.TenantMember]{}, err
	}
	if id != actor.User.TenantID {
		if err := s.record(ctx, actor, "system.tenant.members", "tenant", id, id, nil, nil, false); err != nil {
			return AdminPage[model.TenantMember]{}, err
		}
	}
	return AdminPage[model.TenantMember]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, nil
}
