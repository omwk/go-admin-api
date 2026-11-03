package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/outbox"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"gorm.io/gorm"
)

const adminUserColumns = "sys_user.id, sys_user.tenant_id, sys_user.username, sys_user.nickname, sys_user.email, sys_user.avatar, sys_user.dept_id, sys_user.status, sys_user.is_super, sys_user.created_at, sys_user.updated_at"
const adminRoleColumns = "id, tenant_id, code, name, data_scope, remark, status"
const adminMenuColumns = "id, tenant_id, parent_id, name, type, path, component, redirect, icon, sort, visible, keep_alive, is_external, permission_code, meta, status"

type adminStore struct {
	database    *db.DB
	raw         func(context.Context) (*gorm.DB, context.CancelFunc)
	transaction func(context.Context, func(context.Context) error) error
}

var _ repo.Admin = (*adminStore)(nil)

func newAdminStore(database *db.DB) *adminStore {
	return &adminStore{database: database, raw: database.WithContext, transaction: database.Transaction}
}

func (s *adminStore) EmitAuthorizationInvalidation(ctx context.Context, tenant int64, users []int64) error {
	if _, err := scopeRegistryQuery("Admin.EmitAuthorizationInvalidation"); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"user_ids": users})
	if err != nil {
		return err
	}
	return outbox.Emit(ctx, s.database, outbox.Event{TenantID: tenant, AggregateType: "authorization", AggregateID: fmt.Sprint(tenant), EventType: "authorization.invalidate", Payload: payload})
}

func (s *adminStore) Transaction(ctx context.Context, run func(context.Context) error) error {
	if _, err := scope.Query("Admin.Transaction"); err != nil {
		return err
	}
	return s.transaction(ctx, run)
}

func (s *adminStore) LockManagement(ctx context.Context, tenant int64) error {
	if err := requireAdminTenant(ctx, tenant, "Admin.LockManagement"); err != nil {
		return err
	}
	q, cancel := s.raw(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", fmt.Sprintf("identity:management:%d", tenant)).Error
}

func (s *adminStore) query(ctx context.Context, tenant int64, row any, method string) (*db.ScopedQuery, context.CancelFunc) {
	root, ok := row.(interface{ TableName() string })
	if !ok || !scope.Allows(method, root.TableName()) {
		ctx = scope.WithAccess(ctx, scope.Access{})
	}
	q := scopedAdminQuery(ctx, tenant, row, s.raw)
	return q, q.Close
}

func adminDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		switch postgres.Code {
		case "23505", "23503":
			return errs.New(errs.ErrConflict, 409, "资源冲突或仍被引用")
		case "23514":
			return errs.New(errs.ErrInvalidParam, 400, "参数校验失败")
		}
	}
	return err
}

func scopedMutation(result *db.ScopedQuery, err error) error {
	if err != nil {
		return adminDBError(err)
	}
	if result.RowsAffected() == 0 {
		return errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	return nil
}

func adminMutation(result *gorm.DB) error {
	if result.Error != nil {
		return adminDBError(result.Error)
	}
	if result.RowsAffected == 0 {
		return errs.New(errs.ErrNotFound, 404, "资源不存在")
	}
	return nil
}

func (s *adminStore) User(ctx context.Context, tenant, id int64) (model.User, error) {
	q, cancel := s.query(ctx, tenant, &model.User{}, "Admin.User")
	defer cancel()
	var row model.User
	err := q.Select(adminUserColumns).Where("id = ?", id).Lock().Take(&row)
	return row, adminDBError(err)
}

func (s *adminStore) Role(ctx context.Context, tenant, id int64) (model.Role, error) {
	q, cancel := s.query(ctx, tenant, &model.Role{}, "Admin.Role")
	defer cancel()
	var row model.Role
	err := q.Select(adminRoleColumns).Where("id = ?", id).Lock().Take(&row)
	return row, adminDBError(err)
}

func (s *adminStore) Menu(ctx context.Context, tenant, id int64) (model.Menu, error) {
	q, cancel := s.query(ctx, tenant, &model.Menu{}, "Admin.Menu")
	defer cancel()
	var row model.Menu
	err := q.Select(adminMenuColumns).Where("id = ?", id).Lock().Take(&row)
	return row, adminDBError(err)
}

func (s *adminStore) CreateUser(ctx context.Context, row *model.User) error {
	if err := s.validateUserTarget(ctx, row.ID, row.DepartmentID); err != nil {
		return err
	}
	q, cancel := s.query(ctx, row.TenantID, row, "Admin.CreateUser")
	defer cancel()
	return adminDBError(q.Create(row))
}
func (s *adminStore) CreateRole(ctx context.Context, row *model.Role) error {
	q, cancel := s.query(ctx, row.TenantID, row, "Admin.CreateRole")
	defer cancel()
	return adminDBError(q.Create(row))
}
func (s *adminStore) CreateMenu(ctx context.Context, row *model.Menu) error {
	q, cancel := s.query(ctx, row.TenantID, row, "Admin.CreateMenu")
	defer cancel()
	// Select includes false booleans, overriding model defaults explicitly.
	return adminDBError(q.Select("TenantID", "ParentID", "Name", "Type", "Path", "Component", "Redirect", "Icon", "Sort", "Visible", "KeepAlive", "IsExternal", "PermissionCode", "Meta", "Status").Create(row))
}

func (s *adminStore) UpdateUser(ctx context.Context, tenant, id int64, values map[string]any) error {
	for _, key := range []string{"id", "tenant_id"} {
		if _, ok := values[key]; ok {
			return scope.Denied()
		}
	}
	if raw, exists := values["dept_id"]; exists {
		dept, ok := raw.(int64)
		if !ok || dept <= 0 {
			return scope.Denied()
		}
		if err := s.validateUserTarget(ctx, id, dept); err != nil {
			return err
		}
	}
	q, cancel := s.query(ctx, tenant, &model.User{}, "Admin.UpdateUser")
	defer cancel()
	return scopedMutation(q, q.Where("id = ?", id).Updates(values))
}
func (s *adminStore) UpdateRole(ctx context.Context, tenant, id int64, values map[string]any) error {
	q, cancel := s.query(ctx, tenant, &model.Role{}, "Admin.UpdateRole")
	defer cancel()
	return scopedMutation(q, q.Where("id = ?", id).Updates(values))
}
func (s *adminStore) UpdateMenu(ctx context.Context, tenant, id int64, values map[string]any) error {
	q, cancel := s.query(ctx, tenant, &model.Menu{}, "Admin.UpdateMenu")
	defer cancel()
	return scopedMutation(q, q.Where("id = ?", id).Updates(values))
}
func (s *adminStore) DeleteUser(ctx context.Context, tenant, id int64) error {
	q, cancel := s.query(ctx, tenant, &model.User{}, "Admin.DeleteUser")
	defer cancel()
	return scopedMutation(q, q.Where("id = ?", id).Delete(&model.User{}))
}
func (s *adminStore) DeleteRole(ctx context.Context, tenant, id int64) error {
	q, cancel := s.query(ctx, tenant, &model.Role{}, "Admin.DeleteRole")
	defer cancel()
	err := q.Where("id = ?", id).Delete(&model.Role{})
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && postgres.Code == "23503" {
		return errs.New(20105, 409, "角色仍被引用")
	}
	return scopedMutation(q, err)
}
func (s *adminStore) DeleteMenu(ctx context.Context, tenant, id int64) error {
	q, cancel := s.query(ctx, tenant, &model.Menu{}, "Admin.DeleteMenu")
	defer cancel()
	return scopedMutation(q, q.Where("id = ?", id).Delete(&model.Menu{}))
}
