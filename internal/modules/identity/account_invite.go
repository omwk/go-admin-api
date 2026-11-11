package identity

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// InviteInput preassigns department and roles with optional expiry and use count.
type InviteInput struct {
	DepartmentID int64      `json:"dept_id" binding:"required,gt=0"`
	RoleIDs      []int64    `json:"role_ids"`
	Username     *string    `json:"username" binding:"omitempty,max=256"`
	ExpiresAt    *time.Time `json:"expires_at"`
	MaxUses      *int       `json:"max_uses"`
}

// InviteView exposes safe metadata and a creation-only copyable link.
type InviteView struct {
	model.AuthToken
	Status string `json:"status"`
	Link   string `json:"link,omitempty"`
}

func inviteView(row model.AuthToken, now time.Time) InviteView {
	status := "active"
	switch {
	case row.RevokedAt != nil:
		status = "revoked"
	case row.UsedAt != nil || row.MaxUses > 0 && row.UsedCount >= row.MaxUses:
		status = "used"
	case !row.ExpiresAt.After(now):
		status = "expired"
	}
	return InviteView{AuthToken: row, Status: status}
}

// CreateInvite validates grants using the existing administrator boundary.
func (s *AccountService) CreateInvite(ctx context.Context, actor Overview, in InviteInput) (InviteView, error) {
	if in.DepartmentID <= 0 || !validIDs(in.RoleIDs) {
		return InviteView{}, invalidAdmin()
	}
	now := s.Now().UTC()
	expires := now.Add(7 * 24 * time.Hour)
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC()
	}
	uses := 1
	if in.MaxUses != nil {
		uses = *in.MaxUses
	}
	if !expires.After(now) || uses < 0 || in.Username != nil && (uses != 1 || *in.Username == "") {
		return InviteView{}, invalidAdmin()
	}
	raw, err := refresh.Generate()
	if err != nil {
		return InviteView{}, accountError(err)
	}
	roles, err := json.Marshal(in.RoleIDs)
	if err != nil {
		return InviteView{}, accountError(err)
	}
	if len(in.RoleIDs) == 0 {
		roles = json.RawMessage(`[]`)
	}
	row := model.AuthToken{TenantID: actor.User.TenantID, Purpose: "invite", TokenHash: refresh.Hash(raw), ExpiresAt: expires, CreatedBy: actor.User.ID, CreatedAt: now, DepartmentID: &in.DepartmentID, RoleIDs: roles, Username: in.Username, MaxUses: uses}
	var delivery int64
	err = s.inviteTransaction(ctx, actor, func(tx context.Context) error {
		if err := s.Admin.checkDepartments(tx, actor, []int64{in.DepartmentID}); err != nil {
			return err
		}
		if err := s.Admin.checkRoleIDs(tx, actor, in.RoleIDs); err != nil {
			return err
		}
		if err := s.Store.InsertToken(tx, &row); err != nil {
			return err
		}
		if s.Notify != nil {
			var err error
			delivery, err = s.Notify.Queue(tx, row.TenantID, row.CreatedBy, "invite", row.ID, AccountLink(s.PublicURL, "invite", row.TenantID, raw), "")
			if err != nil {
				return err
			}
		}
		return s.Admin.record(tx, actor, permission.SystemUserCreate, "invite", row.ID, nil, map[string]any{"id": row.ID}, false)
	})
	if err != nil {
		return InviteView{}, accountError(err)
	}
	out := inviteView(row, now)
	out.Link = AccountLink(s.PublicURL, "invite", row.TenantID, raw)
	if delivery > 0 {
		if err := s.Notify.Deliver(ctx, row.TenantID, delivery); err != nil {
			return InviteView{}, accountError(err)
		}
	}
	return out, nil
}

func (s *AccountService) inviteTransaction(ctx context.Context, actor Overview, run func(context.Context) error) error {
	ctx, err := s.Admin.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return err
	}
	return s.Admin.store.Transaction(ctx, func(tx context.Context) error {
		if err := s.Admin.store.LockManagement(tx, actor.User.TenantID); err != nil {
			return err
		}
		tx, err := s.Admin.store.Authorize(tx, actor.User.TenantID, actor.User.ID)
		if err != nil {
			return err
		}
		return run(tx)
	})
}

// Invites returns tenant invites, limited to their creator for ordinary administrators.
func (s *AccountService) Invites(ctx context.Context, actor Overview, page repo.Page) (AdminPage[InviteView], error) {
	creator := actor.User.ID
	if actor.User.IsSuper {
		creator = 0
	}
	rows, total, err := s.Store.Invites(ctx, actor.User.TenantID, creator, page)
	out := AdminPage[InviteView]{List: []InviteView{}, Total: total, Page: page.Number, PageSize: page.Size}
	for _, row := range rows {
		out.List = append(out.List, inviteView(row, s.Now().UTC()))
	}
	return out, accountError(err)
}

// RevokeInvite makes a pending invite unusable under the same row lock as acceptance.
func (s *AccountService) RevokeInvite(ctx context.Context, actor Overview, id int64) error {
	return accountError(s.inviteTransaction(ctx, actor, func(tx context.Context) error {
		row, found, err := s.Store.TokenByID(tx, actor.User.TenantID, id)
		if err != nil {
			return err
		}
		if !found || row.Purpose != "invite" || !actor.User.IsSuper && row.CreatedBy != actor.User.ID {
			return adminNotFound()
		}
		now := s.Now().UTC()
		row.RevokedAt = &now
		if err := s.Store.SaveToken(tx, row); err != nil {
			return err
		}
		return s.Admin.record(tx, actor, permission.SystemUserDelete, "invite", row.ID, nil, nil, false)
	}))
}

// AccountLink targets the frontend; the opaque credential stays in the URL fragment.
func AccountLink(base, purpose string, tenant int64, raw string) string {
	path := "/auth/password/reset"
	if purpose == "invite" {
		path = "/auth/invite/accept"
	}
	return strings.TrimRight(base, "/") + path + "?tenant_id=" + strconv.FormatInt(tenant, 10) + "#token=" + raw
}
