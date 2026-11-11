package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// AdminDepartment is one department node with a non-null children array.
type AdminDepartment struct {
	repo.DepartmentRow
	Children []*AdminDepartment `json:"children"`
}

// User returns the safe account detail under the current effective data scope.
func (s *AdminService) User(ctx context.Context, actor Overview, id int64) (AdminUser, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return AdminUser{}, err
	}
	row, err := s.store.ReadUser(ctx, actor.User.TenantID, id)
	return adminUser(row), err
}

// Role returns tenant-shared role configuration without taking a write lock.
func (s *AdminService) Role(ctx context.Context, actor Overview, id int64) (model.Role, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return model.Role{}, err
	}
	return s.store.ReadRole(ctx, actor.User.TenantID, id)
}

// Menu returns tenant-shared menu configuration without taking a write lock.
func (s *AdminService) Menu(ctx context.Context, actor Overview, id int64) (model.Menu, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return model.Menu{}, err
	}
	return s.store.ReadMenu(ctx, actor.User.TenantID, id)
}

// Links returns only assigned IDs after checking the owner's tenant and scope.
func (s *AdminService) Links(ctx context.Context, actor Overview, kind repo.LinkKind, id int64) ([]int64, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return nil, err
	}
	return s.store.ReadLinks(ctx, actor.User.TenantID, kind, id)
}

// Departments assembles the complete tenant-shared directory from one batch query.
func (s *AdminService) Departments(ctx context.Context, actor Overview) ([]*AdminDepartment, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Departments(ctx, actor.User.TenantID, 0)
	if err != nil {
		return nil, err
	}
	nodes := make(map[int64]*AdminDepartment, len(rows))
	for _, row := range rows {
		nodes[row.ID] = &AdminDepartment{DepartmentRow: row, Children: []*AdminDepartment{}}
	}
	result := []*AdminDepartment{}
	for _, row := range rows {
		node := nodes[row.ID]
		if parent := nodes[row.ParentID]; parent != nil {
			parent.Children = append(parent.Children, node)
		} else {
			result = append(result, node)
		}
	}
	return result, nil
}

// Department reads metadata and its direct member count; missing tenant rows return 404.
func (s *AdminService) Department(ctx context.Context, actor Overview, id int64) (repo.DepartmentRow, error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return repo.DepartmentRow{}, err
	}
	rows, err := s.store.Departments(ctx, actor.User.TenantID, id)
	if err != nil {
		return repo.DepartmentRow{}, err
	}
	if len(rows) == 0 {
		return repo.DepartmentRow{}, adminNotFound()
	}
	return rows[0], nil
}
