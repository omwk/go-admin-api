package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// Roles returns the paginated shared role configuration.
func (s *AdminService) Roles(ctx context.Context, actor Overview, page repo.Page) (AdminPage[model.Role], error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return AdminPage[model.Role]{}, err
	}
	rows, total, err := s.store.Roles(ctx, actor.User.TenantID, page)
	return AdminPage[model.Role]{List: rows, Total: total, Page: page.Number, PageSize: page.Size}, err
}

// CreateRole creates scalar configuration with no implicit authorization grants.
func (s *AdminService) CreateRole(ctx context.Context, actor Overview, request RoleRequest) (model.Role, error) {
	if err := s.checkScope(ctx, actor, request.DataScope); err != nil {
		return model.Role{}, err
	}
	if request.Code == SuperAdminRoleCode {
		return model.Role{}, conflictAdmin()
	}
	row := model.Role{TenantID: actor.User.TenantID, Code: request.Code, Name: request.Name, DataScope: request.DataScope, Remark: request.Remark, Status: request.Status}
	if err := s.store.CreateRole(ctx, &row); err != nil {
		return row, err
	}
	after, err := snapshot(row)
	if err != nil {
		return row, err
	}
	return row, s.record(ctx, actor, permission.SystemRoleCreate, "role", row.ID, nil, after, false)
}

// UpdateRole updates scalar fields; custom department grants use /depts.
func (s *AdminService) UpdateRole(ctx context.Context, actor Overview, id int64, request RoleRequest) (model.Role, error) {
	row, err := s.store.Role(ctx, actor.User.TenantID, id)
	if err != nil {
		return row, err
	}
	if err := s.validateRoleUpdate(ctx, actor, row, request); err != nil {
		return row, err
	}
	before, err := snapshot(row)
	if err != nil {
		return row, err
	}
	values := map[string]any{"code": request.Code, "name": request.Name, "data_scope": request.DataScope, "remark": request.Remark, "status": request.Status}
	if err := s.store.UpdateRole(ctx, actor.User.TenantID, id, values); err != nil {
		return row, err
	}
	updated, err := s.store.Role(ctx, actor.User.TenantID, id)
	if err != nil {
		return updated, err
	}
	after, err := snapshot(updated)
	if err != nil {
		return updated, err
	}
	return updated, s.record(ctx, actor, permission.SystemRoleUpdate, "role", id, before, after, false)
}

func (s *AdminService) validateRoleUpdate(ctx context.Context, actor Overview, row model.Role, request RoleRequest) error {
	if row.Code == SuperAdminRoleCode && (!actor.User.IsSuper || request.Code != SuperAdminRoleCode || request.DataScope != "all") {
		return conflictAdmin()
	}
	if row.Code != SuperAdminRoleCode && request.Code == SuperAdminRoleCode {
		return conflictAdmin()
	}
	if err := s.canManageRole(ctx, actor, row); err != nil {
		return err
	}
	row.DataScope = request.DataScope
	return s.canManageRole(ctx, actor, row)
}

// DeleteRole rejects every reference type and atomically audits the full removed row.
func (s *AdminService) DeleteRole(ctx context.Context, actor Overview, id int64) error {
	row, err := s.store.Role(ctx, actor.User.TenantID, id)
	if err != nil {
		return err
	}
	if row.Code == SuperAdminRoleCode {
		return conflictAdmin()
	}
	references, err := s.store.References(ctx, actor.User.TenantID, "role", id)
	if err != nil {
		return err
	}
	for _, count := range references {
		if count > 0 {
			return &errs.AppError{Code: 20105, HTTPStatus: 409, Message: "角色仍被引用", Details: map[string]any{"references": references}}
		}
	}
	before, err := snapshot(row)
	if err != nil {
		return err
	}
	if err := s.store.DeleteRole(ctx, actor.User.TenantID, id); err != nil {
		return err
	}
	return s.record(ctx, actor, permission.SystemRoleDelete, "role", id, before, nil, true)
}
