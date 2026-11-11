package identity

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// Users returns a safe, paginated tenant user projection.
func (s *AdminService) Users(ctx context.Context, actor Overview, page repo.Page) (AdminPage[AdminUser], error) {
	ctx, err := s.store.Authorize(ctx, actor.User.TenantID, actor.User.ID)
	if err != nil {
		return AdminPage[AdminUser]{}, err
	}
	rows, total, err := s.store.Users(ctx, actor.User.TenantID, page)
	result := AdminPage[AdminUser]{List: []AdminUser{}, Total: total, Page: page.Number, PageSize: page.Size}
	for _, row := range rows {
		result.List = append(result.List, adminUser(row))
	}
	return result, err
}

// CreateUser creates an ordinary account; roles and super-administrator flags are not input fields.
func (s *AdminService) CreateUser(ctx context.Context, actor Overview, request CreateUserRequest) (AdminUser, error) {
	if pwd.Validate(request.Password) != nil {
		return AdminUser{}, invalidPassword()
	}
	if err := s.checkDepartments(ctx, actor, []int64{request.DepartmentID}); err != nil {
		return AdminUser{}, err
	}
	hash, err := pwd.Hash(request.Password)
	if err != nil {
		return AdminUser{}, invalidAdmin()
	}
	row := model.User{TenantID: actor.User.TenantID, Username: request.Username, PasswordHash: hash, Nickname: request.Nickname,
		Email: request.Email, Avatar: request.Avatar, DepartmentID: request.DepartmentID, Status: request.Status, ForcePasswordChange: true}
	if err := s.store.CreateUser(ctx, &row); err != nil {
		return AdminUser{}, err
	}
	if s.accounts != nil {
		if err := rememberInitialPassword(ctx, s.accounts, row, s.accounts.Now().UTC()); err != nil {
			return AdminUser{}, err
		}
	}
	err = s.record(ctx, actor, permission.SystemUserCreate, "user", row.ID, nil, map[string]any{"id": row.ID}, false)
	return adminUser(row), err
}

// UpdateUser modifies only scalars and cannot change roles or credentials.
func (s *AdminService) UpdateUser(ctx context.Context, actor Overview, id int64, request UserFields) (AdminUser, error) {
	row, err := s.store.User(ctx, actor.User.TenantID, id)
	if err != nil {
		return AdminUser{}, err
	}
	if row.IsSuper && !actor.User.IsSuper {
		return AdminUser{}, denied()
	}
	if err := s.checkDepartments(ctx, actor, []int64{request.DepartmentID}); err != nil {
		return AdminUser{}, err
	}
	values := map[string]any{"nickname": request.Nickname, "email": request.Email, "avatar": request.Avatar, "dept_id": request.DepartmentID, "status": request.Status}
	if err := s.store.UpdateUser(ctx, actor.User.TenantID, id, values); err != nil {
		return AdminUser{}, err
	}
	updated, err := s.store.User(ctx, actor.User.TenantID, id)
	if err != nil {
		return AdminUser{}, err
	}
	err = s.record(ctx, actor, permission.SystemUserUpdate, "user", id, map[string]any{"status": row.Status, "dept_id": row.DepartmentID}, map[string]any{"status": updated.Status, "dept_id": updated.DepartmentID}, false)
	return adminUser(updated), err
}

// DeleteUser soft deletes the account, preserving historical references.
func (s *AdminService) DeleteUser(ctx context.Context, actor Overview, id int64) error {
	row, err := s.store.User(ctx, actor.User.TenantID, id)
	if err != nil {
		return err
	}
	if row.Username == SeedAdminUsername {
		return conflictAdmin()
	}
	if row.IsSuper && !actor.User.IsSuper {
		return denied()
	}
	if err := s.store.DeleteUser(ctx, actor.User.TenantID, id); err != nil {
		return err
	}
	return s.record(ctx, actor, permission.SystemUserDelete, "user", id, map[string]any{"id": id}, nil, false)
}

// ResetPassword changes only the password hash and never records plaintext or hash in audit.
func (s *AdminService) ResetPassword(ctx context.Context, actor Overview, id int64, password string) error {
	row, err := s.store.ReadUser(ctx, actor.User.TenantID, id)
	if err != nil {
		return err
	}
	if row.IsSuper && !actor.User.IsSuper {
		return denied()
	}
	if s.accounts != nil {
		if err := s.accounts.AdminResetPassword(ctx, actor.User.TenantID, id, password); err != nil {
			return err
		}
		return s.record(ctx, actor, permission.SystemUserReset, "user", id, nil, nil, false)
	}
	if pwd.Validate(password) != nil {
		return invalidPassword()
	}
	hash, err := pwd.Hash(password)
	if err != nil {
		return invalidAdmin()
	}
	if err := s.store.UpdateUser(ctx, actor.User.TenantID, id, map[string]any{"password_hash": hash, "force_password_change": true}); err != nil {
		return err
	}
	return s.record(ctx, actor, permission.SystemUserReset, "user", id, nil, nil, false)
}
