package identity

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// Menus returns the complete management tree, including hidden/disabled nodes and buttons.
func (s *AdminService) Menus(ctx context.Context, actor Overview) ([]AdminMenu, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Menus(ctx, actor.User.TenantID)
	if err != nil {
		return nil, err
	}
	children := map[int64][]model.Menu{}
	for _, row := range rows {
		children[row.ParentID] = append(children[row.ParentID], row)
	}
	var walk func(int64, int) []AdminMenu
	walk = func(parent int64, depth int) []AdminMenu {
		result := []AdminMenu{}
		if depth > 5 {
			return result
		}
		for _, row := range children[parent] {
			result = append(result, AdminMenu{Menu: row, Children: walk(row.ID, depth+1)})
		}
		return result
	}
	return walk(0, 1), nil
}

func (s *AdminService) validateMenu(ctx context.Context, actor Overview, id int64, request MenuRequest) error {
	if request.ParentID < 0 {
		return invalidAdmin()
	}
	if len(request.Meta) > 0 {
		var object map[string]any
		if json.Unmarshal(request.Meta, &object) != nil || object == nil {
			return invalidAdmin()
		}
	}
	if request.PermissionCode != nil {
		if err := permission.Registered(*request.PermissionCode); err != nil {
			return invalidAdmin()
		}
		rows, err := s.store.Permissions(ctx, actor.User.TenantID)
		if err != nil {
			return err
		}
		found := false
		for _, row := range rows {
			if row.Code == *request.PermissionCode {
				found = true
			}
		}
		if !found {
			return adminNotFound()
		}
	}
	// Execute holds the shared tenant advisory lock before this full, unfiltered graph read.
	rows, err := s.store.Menus(ctx, actor.User.TenantID)
	if err != nil {
		return err
	}
	return ValidateMenuMove(rows, id, request.ParentID)
}

// CreateMenu validates the parent and complete resulting tree before inserting a route.
func (s *AdminService) CreateMenu(ctx context.Context, actor Overview, request MenuRequest) (model.Menu, error) {
	if err := s.validateMenu(ctx, actor, 0, request); err != nil {
		return model.Menu{}, err
	}
	row := request.row(actor.User.TenantID, 0)
	if err := s.store.CreateMenu(ctx, &row); err != nil {
		return row, err
	}
	after, err := snapshot(row)
	if err != nil {
		return row, err
	}
	return row, s.record(ctx, actor, permission.SystemMenuCreate, "menu", row.ID, nil, after, false)
}

// UpdateMenu re-reads all nodes inside the transaction lock and validates the whole subtree.
func (s *AdminService) UpdateMenu(ctx context.Context, actor Overview, id int64, request MenuRequest) (model.Menu, error) {
	row, err := s.store.Menu(ctx, actor.User.TenantID, id)
	if err != nil {
		return row, err
	}
	if err := s.validateMenu(ctx, actor, id, request); err != nil {
		return row, err
	}
	before, err := snapshot(row)
	if err != nil {
		return row, err
	}
	updated := request.row(actor.User.TenantID, id)
	values, err := snapshot(updated)
	if err != nil {
		return row, err
	}
	delete(values, "id")
	delete(values, "tenant_id")
	// Preserve JSON as JSONB rather than converting RawMessage through an untyped map.
	values["meta"] = updated.Meta
	if err := s.store.UpdateMenu(ctx, actor.User.TenantID, id, values); err != nil {
		return row, err
	}
	after, err := snapshot(updated)
	if err != nil {
		return row, err
	}
	return updated, s.record(ctx, actor, permission.SystemMenuUpdate, "menu", id, before, after, false)
}

// DeleteMenu reports children/roles separately before removal and records a full audit snapshot.
func (s *AdminService) DeleteMenu(ctx context.Context, actor Overview, id int64) error {
	row, err := s.store.Menu(ctx, actor.User.TenantID, id)
	if err != nil {
		return err
	}
	refs, err := s.store.References(ctx, actor.User.TenantID, "menu", id)
	if err != nil {
		return err
	}
	for _, key := range []string{"children", "roles"} {
		if refs[key] > 0 {
			return &errs.AppError{Code: errs.ErrConflict, HTTPStatus: 409, Message: "菜单仍被引用", Details: map[string]any{key: refs[key]}}
		}
	}
	before, err := snapshot(row)
	if err != nil {
		return err
	}
	if err := s.store.DeleteMenu(ctx, actor.User.TenantID, id); err != nil {
		return err
	}
	return s.record(ctx, actor, permission.SystemMenuDelete, "menu", id, before, nil, true)
}

// Permissions returns the tenant's stored registry entries grouped by module.
func (s *AdminService) Permissions(ctx context.Context, actor Overview) (map[string][]model.Permission, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Permissions(ctx, actor.User.TenantID)
	if err != nil {
		return nil, err
	}
	result := map[string][]model.Permission{}
	for _, row := range rows {
		result[row.Module] = append(result[row.Module], row)
	}
	return result, nil
}
