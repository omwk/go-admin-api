package bootstrap

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantTemplateIntegrationOptionalSourceAndLimits(t *testing.T) {
	// BE-IDN-010 §3.1–2: optional null source, all scopes and inclusive field/count limits.
	f := newTenantFixture(t)
	w, out := f.request(t, "POST", "/admin/tenant-templates", map[string]any{"code": "null-source", "name": "空白", "source_tenant_id": nil}, "")
	require.Equal(t, 201, w.Code)
	var row model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &row))
	require.JSONEq(t, "[]", string(row.Roles))
	for _, source := range []int64{0, -1} {
		w, _ = f.request(t, "POST", "/admin/tenant-templates", map[string]any{"code": "bad-source", "name": "bad", "source_tenant_id": source}, "")
		require.Equal(t, 400, w.Code)
	}
	w, _ = f.request(t, "POST", "/admin/tenant-templates", map[string]any{"code": "absent-source", "name": "absent", "source_tenant_id": 999999}, "")
	require.Equal(t, 404, w.Code)
	roles := make([]model.TemplateRole, 50)
	scopes := []string{"all", "dept", "dept_and_child", "self", "custom"}
	for i := range roles {
		roles[i] = model.TemplateRole{Code: fmt.Sprintf("role_%d", i), Name: strings.Repeat("名", 128), DataScope: scopes[i%len(scopes)], Permissions: []string{}, Menus: []string{}}
	}
	roles[0].Code, roles[0].Status = strings.Repeat("a", 64), "disabled"
	changed := putTenantTemplateRoles(t, f, row.ID, roles)
	for i := 1; i < len(roles); i++ {
		roles[i].Status = "active"
	}
	assertTemplateRoles(t, changed, roles)
	for _, value := range []any{map[string]any{}, []any{}, "changed"} {
		w, _ = f.request(t, "PUT", templatePath(row.ID), map[string]any{"name": "forbidden", "menus": value}, "")
		require.Equal(t, 400, w.Code)
	}
	w, out = f.request(t, "GET", templatePath(row.ID), nil, "")
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(out.Data, &row))
	assertTemplateRoles(t, row, roles)
	t.Log("null source201; zero/negative source400; absent source404; 50 roles, code64/name128 and all five scopes accepted; status defaults active; PUT menus rejected")
}

func TestIdentityTenantTemplateIntegrationLegacyMenuKeys(t *testing.T) {
	// BE-IDN-010 §4.9: keys for pathless and duplicate-path nodes still round-trip.
	f := newTenantFixture(t)
	addTenantSnapshotEdges(t, f)
	row := f.template(t, "legacy-menu-keys")
	var menus []model.TemplateMenu
	require.NoError(t, json.Unmarshal(row.Menus, &menus))
	keys := []string{}
	wantedNames := []string{}
	for _, menu := range menus {
		if menu.Name == "pathless-button" || menu.Name == "duplicate-one" {
			keys = append(keys, menu.Key)
			wantedNames = append(wantedNames, menu.Name)
		}
	}
	require.Len(t, keys, 2)
	roles := readonlyTemplateRoles()[:1]
	roles[0].Menus = keys
	changed := putTenantTemplateRoles(t, f, row.ID, roles)
	assertTemplateRoles(t, changed, roles)
	result := f.provision(t, row.ID, "legacy-menu-tenant")
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var names []string
	require.NoError(t, q.Table("sys_role_menu rm").Select("m.name").Joins("JOIN sys_menu m ON m.tenant_id=rm.tenant_id AND m.id=rm.menu_id").Where("rm.tenant_id=?", result.TenantID).Scan(&names).Error)
	require.ElementsMatch(t, wantedNames, names)
	t.Log("legacy pathless/duplicate menu keys accepted and materialized with exact selected menu names")
}

func TestIdentityTenantTemplateIntegrationCloneValidation(t *testing.T) {
	f := newTenantFixture(t)
	row := blankTenantTemplate(t, f, "clone-validation")
	path := templatePath(row.ID) + "/clone"
	for _, body := range []map[string]any{
		{"code": "valid"}, {"code": "Invalid", "name": "invalid"},
		{"code": "valid", "name": "  "}, {"code": "valid", "name": strings.Repeat("名", 129)},
		{"code": "valid", "name": "valid", "roles": []any{}},
	} {
		w, _ := f.request(t, "POST", path, body, "")
		require.Equal(t, 400, w.Code)
	}
	w, _ := f.request(t, "POST", templatePath(999999)+"/clone", map[string]any{"code": "missing", "name": "missing"}, "")
	require.Equal(t, 404, w.Code)
	w, _ = f.request(t, "POST", path, map[string]any{"code": "clone-once", "name": "once"}, "clone-once")
	require.Equal(t, 201, w.Code)
	w, out := f.request(t, "POST", path, map[string]any{"code": "clone-twice", "name": "twice"}, "clone-once")
	require.Equal(t, 409, w.Code)
	require.Equal(t, 10006, out.Code)
}
