package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestAdminIntegrationRoleDeletionChecksAllReferences(t *testing.T) {
	// REQ-IAM-005: all four link tables prevent deletion, including grants without users.
	f := newAdminFixture(t)
	menu := createAdminMenu(t, f, "reference", 0)
	points, err := f.store.Permissions(f.ctx, 1)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		kind   repo.LinkKind
		target int64
	}{
		{"users", repo.UserRoles, f.user.ID}, {"menus", repo.RoleMenus, menu.ID}, {"permissions", repo.RolePermissions, points[0].ID}, {"depts", repo.RoleDepartments, f.user.DepartmentID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role := createAdminRole(t, f, "ref_"+tc.name)
			owner, target := role.ID, tc.target
			if tc.kind == repo.UserRoles {
				owner, target = tc.target, role.ID
			}
			require.NoError(t, f.store.Transaction(f.ctx, func(ctx context.Context) error { return f.store.ReplaceLinks(ctx, 1, tc.kind, owner, []int64{target}) }))
			response, envelope := adminRequest(t, f, "DELETE", resourcePath("roles", role.ID), nil, "")
			require.Equal(t, 409, response.Code, response.Body.String())
			require.Equal(t, 20105, envelope.Code)
		})
	}
	var super model.Role
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Select("id").Where("code = ?", identity.SuperAdminRoleCode).Take(&super).Error)
	response, envelope := adminRequest(t, f, "DELETE", resourcePath("roles", super.ID), nil, "")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10004, envelope.Code)
	response, envelope = adminRequest(t, f, "DELETE", resourcePath("users", f.actor.User.ID), nil, "")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10004, envelope.Code)
}

func TestAdminIntegrationMenuDeletionExplainsReference(t *testing.T) {
	f := newAdminFixture(t)
	parent := createAdminMenu(t, f, "parent", 0)
	child := createAdminMenu(t, f, "child", parent.ID)
	response, envelope := adminRequest(t, f, "DELETE", resourcePath("menus", parent.ID), nil, "")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10004, envelope.Code)
	require.Equal(t, map[string]any{"children": float64(1)}, envelope.Details)
	role := createAdminRole(t, f, "menu_ref")
	response, _ = adminRequest(t, f, "PUT", resourcePath("roles", role.ID)+"/menus", map[string]any{"menu_ids": []int64{child.ID}}, "")
	require.Equal(t, 204, response.Code)
	response, envelope = adminRequest(t, f, "DELETE", resourcePath("menus", child.ID), nil, "")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10004, envelope.Code)
	require.Equal(t, map[string]any{"roles": float64(1)}, envelope.Details)
}

func TestAdminIntegrationPhysicalDeletionAuditsFullRows(t *testing.T) {
	f := newAdminFixture(t)
	role := createAdminRole(t, f, "snapshot")
	menu := createAdminMenu(t, f, "snapshot", 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, tc := range []struct {
		resource, table string
		id              int64
	}{{"roles", "sys_role", role.ID}, {"menus", "sys_menu", menu.ID}} {
		var original struct{ Snapshot json.RawMessage }
		require.NoError(t, q.Table(tc.table+" AS r").Select("to_jsonb(r) AS snapshot").Where("tenant_id = ? AND id = ?", 1, tc.id).Take(&original).Error)
		response, _ := adminRequest(t, f, "DELETE", resourcePath(tc.resource, tc.id), nil, "")
		require.Equal(t, 204, response.Code, response.Body.String())
		var event struct{ Before, After json.RawMessage }
		require.NoError(t, q.Table("audit_log").Select("\"before\", \"after\"").Where("tenant_id = ? AND resource_type = ? AND resource_id = ? AND action LIKE ?", 1, tc.resource[:len(tc.resource)-1], strconv.FormatInt(tc.id, 10), "%:delete").Take(&event).Error)
		require.JSONEq(t, string(original.Snapshot), string(event.Before))
		require.JSONEq(t, "null", string(event.After))
		var count int64
		require.NoError(t, q.Table(tc.table).Where("id = ?", tc.id).Count(&count).Error)
		require.Zero(t, count)
		response, _ = adminRequest(t, f, "DELETE", resourcePath(tc.resource, tc.id), nil, "")
		require.Equal(t, 404, response.Code)
	}
}

func TestAdminIntegrationAuditFailureRollsBackPhysicalDelete(t *testing.T) {
	f := newAdminFixture(t)
	role := createAdminRole(t, f, "rollback")
	failure := errors.New("injected audit failure")
	service, err := identity.NewAdminService(f.store, func(context.Context, audit.Event) error { return failure })
	require.NoError(t, err)
	_, err = service.Execute(f.ctx, f.actor, "delete-role", "key", "hash", func(ctx context.Context) (repo.SavedResponse, error) {
		return repo.SavedResponse{Status: 204}, service.DeleteRole(ctx, f.actor, role.ID)
	})
	require.ErrorIs(t, err, failure)
	_, err = f.store.Role(f.ctx, 1, role.ID)
	require.NoError(t, err)
	_, found, err := f.store.Replay(f.ctx, 1, "delete-role", "key", "hash")
	require.NoError(t, err)
	require.False(t, found)
}
