package identity

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/omwk/go-admin-api/internal/modules/identity/authz"
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
	ID             int64      `json:"id"`
	TenantID       int64      `json:"tenant_id"`
	TenantCode     string     `json:"tenant_code"`
	Username       string     `json:"username"`
	Nickname       *string    `json:"nickname,omitempty"`
	Email          *string    `json:"email"`
	Phone          *string    `json:"phone"`
	Avatar         *string    `json:"avatar,omitempty"`
	DepartmentID   int64      `json:"dept_id"`
	DepartmentName *string    `json:"dept_name"`
	IsSuper        bool       `json:"is_super"`
	Roles          []RoleView `json:"roles"`
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

// ProfileService projects current identity and versioned permissions/navigation with uncached account checks.
type ProfileService struct {
	store   repo.Profile
	cache   *authz.Cache
	account func(context.Context, int64, int64) error
}

// UseAuthz configures versioned reads and an uncached current-account check during assembly.
func (s *ProfileService) UseAuthz(cache *authz.Cache, account func(context.Context, int64, int64) error) {
	s.cache = cache
	s.account = account
}

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
	if s.cache != nil {
		if err := s.account(ctx, tenant, user); err != nil {
			return Overview{}, err
		}
		data, err := s.cache.Load(ctx, tenant, user, "perm", func(ctx context.Context) ([]byte, error) {
			value, err := s.readOverview(ctx, tenant, user)
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		})
		if err != nil {
			return Overview{}, dependencyFailure(err)
		}
		var result Overview
		if err := json.Unmarshal(data, &result); err != nil {
			return Overview{}, dependencyFailure(err)
		}
		var fields struct {
			User map[string]json.RawMessage `json:"user"`
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			return Overview{}, dependencyFailure(err)
		}
		if result.User.TenantCode == "" || !hasProfileContactFields(fields.User) {
			return s.readOverview(ctx, tenant, user)
		}
		return result, nil
	}
	return s.readOverview(ctx, tenant, user)
}

func hasProfileContactFields(fields map[string]json.RawMessage) bool {
	return fields["email"] != nil && fields["phone"] != nil && fields["dept_name"] != nil
}

func (s *ProfileService) readOverview(ctx context.Context, tenant, user int64) (Overview, error) {
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
	result := Overview{User: UserView{
		ID: first.ID, TenantID: first.TenantID, TenantCode: first.TenantCode, Username: first.Username,
		Nickname: first.Nickname, Email: first.Email, Phone: first.Phone, Avatar: first.Avatar,
		DepartmentID: first.DepartmentID, DepartmentName: first.DepartmentName,
		IsSuper: first.IsSuper, Roles: []RoleView{},
	}, Permissions: []string{}}
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
	if s.cache != nil {
		data, err := s.cache.Load(ctx, tenant, user, "menu", func(ctx context.Context) ([]byte, error) {
			rows, err := s.readMenus(ctx, tenant, user, overview.User.IsSuper)
			if err != nil {
				return nil, err
			}
			return json.Marshal(rows)
		})
		if err != nil {
			return MenusResponse{}, dependencyFailure(err)
		}
		var rows []MenuNode
		if err := json.Unmarshal(data, &rows); err != nil {
			return MenusResponse{}, dependencyFailure(err)
		}
		return MenusResponse{Overview: overview, Menus: rows}, nil
	}
	rows, err := s.readMenus(ctx, tenant, user, overview.User.IsSuper)
	return MenusResponse{Overview: overview, Menus: rows}, err
}

func (s *ProfileService) readMenus(ctx context.Context, tenant, user int64, super bool) ([]MenuNode, error) {
	rows, err := s.store.Menus(ctx, tenant)
	if err != nil {
		return nil, dependencyFailure(err)
	}
	granted, err := s.store.RoleMenus(ctx, tenant, user)
	if err != nil {
		return nil, dependencyFailure(err)
	}
	return BuildMenuTree(rows, granted, super), nil
}
