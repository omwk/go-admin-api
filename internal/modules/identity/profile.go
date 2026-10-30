package identity

import (
	"context"
	"errors"
	"sort"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// RoleView is the public role subset shared by me and menus.
type RoleView struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// UserView intentionally has no credential or password-hash field.
type UserView struct {
	ID           int64      `json:"id"`
	TenantID     int64      `json:"tenant_id"`
	Username     string     `json:"username"`
	Nickname     *string    `json:"nickname,omitempty"`
	Avatar       *string    `json:"avatar,omitempty"`
	DepartmentID int64      `json:"dept_id"`
	IsSuper      bool       `json:"is_super"`
	Roles        []RoleView `json:"roles"`
}

// Overview is the common source of identity and permissions for both read endpoints.
type Overview struct {
	User        UserView `json:"user"`
	Permissions []string `json:"permissions"`
}

// MenusResponse adds the role-filtered navigation tree to the same overview.
type MenusResponse struct {
	Overview
	Menus []MenuNode `json:"menus"`
}

// ProfileService reads current user state without implementing M1b authorization or caching.
type ProfileService struct{ store repo.Profile }

// NewProfileService constructs the authenticated read service.
func NewProfileService(store repo.Profile) (*ProfileService, error) {
	if store == nil {
		return nil, errors.New("identity read store is required")
	}
	return &ProfileService{store: store}, nil
}

// Me returns the same identity/permission projection used by Menus.
func (s *ProfileService) Me(ctx context.Context, tenant, user int64) (Overview, error) {
	if tenant <= 0 || user <= 0 {
		return Overview{}, revokedToken()
	}
	rows, err := s.store.Identity(ctx, tenant, user)
	if err != nil {
		return Overview{}, dependencyFailure(err)
	}
	if len(rows) == 0 {
		return Overview{}, revokedToken()
	}
	return projectOverview(rows)
}

func projectOverview(rows []repo.IdentityRow) (Overview, error) {
	first := rows[0]
	result := Overview{User: UserView{ID: first.ID, TenantID: first.TenantID, Username: first.Username, Nickname: first.Nickname, Avatar: first.Avatar, DepartmentID: first.DepartmentID, IsSuper: first.IsSuper, Roles: []RoleView{}}, Permissions: []string{}}
	roles, codes := map[int64]RoleView{}, map[string]struct{}{}
	for _, row := range rows {
		if row.RoleID != nil && row.RoleCode != nil && row.RoleName != nil {
			roles[*row.RoleID] = RoleView{ID: *row.RoleID, Code: *row.RoleCode, Name: *row.RoleName}
		}
		if row.PermissionCode == nil {
			continue
		}
		if row.PermissionModule == nil {
			return Overview{}, dependencyFailure(errors.New("permission module is missing"))
		}
		if err := permission.Validate(*row.PermissionModule, *row.PermissionCode); err != nil {
			return Overview{}, dependencyFailure(err)
		}
		codes[*row.PermissionCode] = struct{}{}
	}
	for _, role := range roles {
		result.User.Roles = append(result.User.Roles, role)
	}
	for code := range codes {
		result.Permissions = append(result.Permissions, code)
	}
	sort.Slice(result.User.Roles, func(i, j int) bool { return result.User.Roles[i].ID < result.User.Roles[j].ID })
	sort.Strings(result.Permissions)
	return result, nil
}

// Menus makes exactly two menu reads after loading the shared identity projection.
func (s *ProfileService) Menus(ctx context.Context, tenant, user int64) (MenusResponse, error) {
	overview, err := s.Me(ctx, tenant, user)
	if err != nil {
		return MenusResponse{}, err
	}
	rows, err := s.store.Menus(ctx, tenant)
	if err != nil {
		return MenusResponse{}, dependencyFailure(err)
	}
	granted, err := s.store.RoleMenus(ctx, tenant, user)
	if err != nil {
		return MenusResponse{}, dependencyFailure(err)
	}
	return MenusResponse{Overview: overview, Menus: BuildMenuTree(rows, granted, overview.User.IsSuper)}, nil
}
