package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestIdentityRegistrationIntegrationSettingsValidationAndIsolation(t *testing.T) {
	f := newRegistrationFixture(t)
	other := registrationTenant(t, f, "settings-foreign")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	foreignRole := model.Role{TenantID: other, Code: "foreign-default", Name: "foreign", Status: "active", DataScope: "self"}
	require.NoError(t, q.Create(&foreignRole).Error)
	require.NoError(t, f.database.Exec(f.ctx, `UPDATE tenant SET settings='{"unrelated":"preserved"}'::jsonb WHERE id=1`))
	for _, input := range []map[string]any{
		{"allow_self_registration": "true"}, {"registration_hourly_limit": 0}, {"registration_hourly_limit": 1.5},
		{"tenant_id": other}, {"allow_self_registration": true, "default_role_code": foreignRole.Code},
	} {
		w, out := f.call(t, "PUT", "/admin/settings/tenant", input)
		require.Equal(t, 400, w.Code, out.Code, out.Message)
	}
	f.settings(t, map[string]any{"allow_self_registration": true, "registration_hourly_limit": 12})
	var row struct{ Settings json.RawMessage }
	require.NoError(t, q.Table("tenant").Select("settings").Where("id=1").Take(&row).Error)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(row.Settings, &stored))
	require.Equal(t, "preserved", stored["unrelated"])
	require.Equal(t, true, stored["allow_self_registration"])
	require.EqualValues(t, 12, stored["registration_hourly_limit"])
	require.NoError(t, q.Table("tenant").Select("settings").Where("id=?", other).Take(&row).Error)
	stored = map[string]any{}
	require.NoError(t, json.Unmarshal(row.Settings, &stored))
	require.Len(t, stored, 1)
	require.Equal(t, true, stored["allow_self_registration"])
	w, out := f.call(t, "GET", "/admin/settings/tenant", nil)
	require.Equal(t, 200, w.Code, out.Code, out.Message)
	require.EqualValues(t, 12, systemGroupValue(t, out.Data, "registration_hourly_limit"))
	t.Log("tenant JSONB only; registered type/range validation; foreign role rejected atomically; unrelated keys and other tenant settings preserved")
}

func TestIdentityRegistrationIntegrationSettingsPermissions(t *testing.T) {
	f := newRegistrationFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=false WHERE id=?", f.user.ID))
	grantRegistrationSetting(t, f, permission.SystemSettingList)
	for _, method := range []string{"GET", "PUT"} {
		w, _ := f.call(t, method, "/admin/settings/tenant", map[string]any{"allow_self_registration": true})
		require.Equal(t, 403, w.Code)
	}
	list := f.request(t, "GET", "/admin/settings", f.token, nil)
	require.Equal(t, 200, list.Code)
	var envelope struct{ Data []struct{ Group string } }
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &envelope))
	for _, group := range envelope.Data {
		require.NotEqual(t, "tenant", group.Group)
	}
	grantRegistrationSetting(t, f, permission.SystemSettingUpdate)
	w, out := f.call(t, "GET", "/admin/settings/tenant", nil)
	require.Equal(t, 200, w.Code, out.Code, out.Message)
	f.settings(t, map[string]any{"allow_self_registration": true})
	require.NoError(t, f.database.Exec(f.ctx, `DELETE FROM sys_role_permission WHERE tenant_id=1 AND permission_id IN
 (SELECT id FROM sys_permission WHERE tenant_id=1 AND code=?)`, permission.SystemSettingUpdate))
	grantRegistrationSetting(t, f, permission.SystemTenantManage)
	w, out = f.call(t, "GET", "/admin/settings/tenant", nil)
	require.Equal(t, 200, w.Code, out.Code, out.Message)
	f.settings(t, map[string]any{"allow_self_registration": false})
	t.Log("list-only actor cannot read/update tenant settings, including group list; setting:update or tenant:manage each independently permits GET and PUT")
}

func grantRegistrationSetting(t *testing.T, f *registrationFixture, code string) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	p := model.Permission{TenantID: 1, Code: code, Name: code, Module: "system", Type: "api"}
	require.NoError(t, q.Create(&p).Error)
	var role model.Role
	require.NoError(t, q.Where("tenant_id=1 AND code='account-owner'").Take(&role).Error)
	require.NoError(t, q.Create(&model.RolePermission{TenantID: 1, RoleID: role.ID, PermissionID: p.ID}).Error)
}
