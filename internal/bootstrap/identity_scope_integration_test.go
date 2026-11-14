package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	"github.com/stretchr/testify/require"
)

func TestScopeIntegrationFiveOperationsAndOwnerIsolation(t *testing.T) {
	// REQ-IAM-008 / BE-IDN-006: ordinary users retain every role scope restriction.
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Model(&model.User{}).Where("id=?", f.actor.User.ID).Update("is_super", false).Error)
	own := f.actor.User.DepartmentID
	require.NoError(t, q.Model(&model.Department{}).Where("tenant_id = ? AND id = ?", 1, own).Update("path", "1.2").Error)
	depts := []model.Department{{TenantID: 1, Name: "child", Path: "1.2.3", Status: "active"}, {TenantID: 1, Name: "lookalike", Path: "1.20", Status: "active"}, {TenantID: 1, Name: "other", Path: "1.9", Status: "active"}}
	require.NoError(t, q.Create(&depts).Error)
	users := []model.User{{TenantID: 1, Username: "scope-own", DepartmentID: own, PasswordHash: f.user.PasswordHash, Status: "active"}}
	for i, dept := range depts {
		users = append(users, model.User{TenantID: 1, Username: fmt.Sprintf("scope-%d", i), DepartmentID: dept.ID, PasswordHash: f.user.PasswordHash, Status: "active"})
	}
	require.NoError(t, q.Create(&users).Error)
	for _, kind := range []string{"all", "dept", "dept_and_child", "self", "custom"} {
		t.Run(kind, func(t *testing.T) {
			a := scope.Access{Tenant: 1, User: f.actor.User.ID, Department: own, Path: "1.2", Roles: []scope.Role{{Kind: kind, Departments: []int64{depts[2].ID}}}}
			ctx := scope.WithAccess(f.ctx, a)
			rows, total, err := f.store.Users(ctx, 1, repo.Page{Number: 1, Size: 100, Sort: "-created_at"})
			require.NoError(t, err)
			require.Equal(t, int64(len(rows)), total)
			ids := []int64{}
			for _, row := range rows {
				ids = append(ids, row.ID)
			}
			require.Equal(t, kind != "custom", slices.Contains(ids, f.actor.User.ID))
			for i, user := range users {
				allowed := kind == "all" || kind == "dept" && i == 0 || kind == "dept_and_child" && i <= 1 || kind == "custom" && i == 3
				require.Equal(t, allowed, slices.Contains(ids, user.ID), "list owner %d", user.ID)
				_, err := f.store.User(ctx, 1, user.ID)
				if allowed {
					require.NoError(t, err)
				} else {
					require.Equal(t, 404, errs.From(err).HTTPStatus)
				}
				checkScopedMutation(ctx, t, f, user.ID, allowed, false)
				checkScopedMutation(ctx, t, f, user.ID, allowed, true)
			}
		})
	}
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	response, _ := adminRequest(t, f, "GET", "/users", nil, "")
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"total":1`)
	response, _ = adminRequest(t, f, "DELETE", resourcePath("users", users[0].ID), nil, "")
	require.Equal(t, 404, response.Code)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ?", 1).Update("status", "disabled").Error)
	response, envelope := adminRequest(t, f, "GET", "/users", nil, "")
	require.Equal(t, 403, response.Code)
	require.Equal(t, errs.ErrPermissionDenied, envelope.Code)
	_, err := f.store.Authorize(f.ctx, 1, f.actor.User.ID)
	require.Equal(t, errs.ErrDataScopeDenied, errs.From(err).Code)
}

func checkScopedMutation(ctx context.Context, t *testing.T, f *adminFixture, id int64, allowed, remove bool) {
	t.Helper()
	rollback := errors.New("scope assertion rollback")
	err := f.store.Transaction(ctx, func(ctx context.Context) error {
		var err error
		if remove {
			err = f.store.DeleteUser(ctx, 1, id)
		} else {
			err = f.store.UpdateUser(ctx, 1, id, map[string]any{"status": "disabled"})
		}
		if allowed {
			require.NoError(t, err)
		} else {
			require.Equal(t, 404, errs.From(err).HTTPStatus)
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
}

func TestScopeIntegrationUnionEmptyCustomAndWriteTargets(t *testing.T) {
	f := newAdminFixture(t)
	a := scope.Access{Tenant: 1, User: f.actor.User.ID, Department: f.actor.User.DepartmentID, Roles: []scope.Role{{Kind: "custom"}}}
	ctx := scope.WithAccess(f.ctx, a)
	rows, count, err := f.store.Users(ctx, 1, repo.Page{Number: 1, Size: 100, Sort: "-created_at"})
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, count)
	a.Roles = append(a.Roles, scope.Role{Kind: "self"})
	ctx = scope.WithAccess(f.ctx, a)
	_, count, err = f.store.Users(ctx, 1, repo.Page{Number: 1, Size: 100, Sort: "-created_at"})
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	a.Roles = append(a.Roles, scope.Role{Kind: "all"})
	ctx = scope.WithAccess(f.ctx, a)
	_, count, err = f.store.Users(ctx, 1, repo.Page{Number: 1, Size: 100, Sort: "-created_at"})
	require.NoError(t, err)
	require.Greater(t, count, int64(1))
	a.Roles = []scope.Role{{Kind: "dept_and_child"}}
	a.Path = ""
	ctx = scope.WithAccess(f.ctx, a)
	_, count, err = f.store.Users(ctx, 1, repo.Page{Number: 1, Size: 100, Sort: "-created_at"})
	require.NoError(t, err)
	require.Zero(t, count)
	a.Roles = []scope.Role{{Kind: "self"}}
	ctx = scope.WithAccess(f.ctx, a)
	require.Equal(t, 20108, errs.From(f.store.CreateUser(ctx, &model.User{TenantID: 1, DepartmentID: a.Department})).Code)
	require.Equal(t, 20108, errs.From(f.store.UpdateUser(ctx, 1, a.User, map[string]any{"dept_id": f.user.DepartmentID})).Code)
	require.Equal(t, 20108, errs.From(f.store.UpdateUser(ctx, 1, a.User, map[string]any{"tenant_id": int64(2)})).Code)
}
