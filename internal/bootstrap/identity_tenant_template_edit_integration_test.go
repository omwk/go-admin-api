package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

func TestIdentityTenantTemplateIntegrationBlankAndEdit(t *testing.T) {
	// BE-IDN-010 §4.1–2: editable roles round-trip while framework menus stay fixed.
	f := newTenantFixture(t)
	row := blankTenantTemplate(t, f, "readonly")
	roles := readonlyTemplateRoles()
	changed := putTenantTemplateRoles(t, f, row.ID, roles)
	w, out := f.request(t, "GET", templatePath(row.ID), nil, "")
	require.Equal(t, 200, w.Code)
	var detail model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &detail))
	assertTemplateRoles(t, detail, roles)
	require.JSONEq(t, string(row.Menus), string(changed.Menus))
	require.JSONEq(t, string(row.Menus), string(detail.Menus))
	roles[0].Permissions = append(roles[0].Permissions, roles[0].Permissions...)
	roles[0].Menus = append(roles[0].Menus, roles[0].Menus...)
	changed = putTenantTemplateRoles(t, f, row.ID, roles)
	assertTemplateRoles(t, changed, readonlyTemplateRoles())
	t.Log("1 blank: roles=[] menus nonempty; 2 two roles GET=PUT; duplicate grants removed; menus unchanged")
}

func TestIdentityTenantTemplateIntegrationRoleValidation(t *testing.T) {
	// BE-IDN-010 §4.3–5: every rejected replacement leaves the saved roles intact.
	f := newTenantFixture(t)
	row := blankTenantTemplate(t, f, "validation")
	for _, tc := range []struct {
		name string
		edit func(*model.TemplateRole)
		want string
	}{
		{"unknown_permission", func(r *model.TemplateRole) { r.Permissions = []string{"trade:order:unknown"} }, "trade:order:unknown"},
		{"unknown_menu", func(r *model.TemplateRole) { r.Menus = []string{"/not-in-template"} }, ""},
		{"empty_code", func(r *model.TemplateRole) { r.Code = "" }, ""},
		{"invalid_code", func(r *model.TemplateRole) { r.Code = "has-hyphen" }, ""},
		{"long_code", func(r *model.TemplateRole) { r.Code = strings.Repeat("a", 65) }, ""},
		{"blank_name", func(r *model.TemplateRole) { r.Name = "  " }, ""},
		{"long_name", func(r *model.TemplateRole) { r.Name = strings.Repeat("名", 129) }, ""},
		{"invalid_scope", func(r *model.TemplateRole) { r.DataScope = "tenant" }, ""},
		{"invalid_status", func(r *model.TemplateRole) { r.Status = "locked" }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles := readonlyTemplateRoles()
			tc.edit(&roles[0])
			w, out := f.request(t, "PUT", templatePath(row.ID), map[string]any{"name": "invalid", "roles": roles}, "")
			require.Equal(t, 400, w.Code)
			require.Equal(t, 10001, out.Code)
			if tc.want != "" {
				require.Contains(t, w.Body.String(), tc.want)
			}
		})
	}
	for _, roles := range [][]model.TemplateRole{{readonlyTemplateRoles()[0], readonlyTemplateRoles()[0]}, make([]model.TemplateRole, 51)} {
		w, _ := f.request(t, "PUT", templatePath(row.ID), map[string]any{"name": "invalid", "roles": roles}, "")
		require.Equal(t, 400, w.Code)
	}
	w, out := f.request(t, "GET", templatePath(row.ID), nil, "")
	require.Equal(t, 200, w.Code)
	var saved model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &saved))
	require.JSONEq(t, "[]", string(saved.Roles))
	require.Equal(t, row.Name, saved.Name)
	t.Log("3 unknown permission named in 400; 4 missing menu 400; 5 duplicate code 400; failed writes atomic")
}

func blankTenantTemplate(t *testing.T, f *tenantFixture, code string) model.TenantTemplate {
	t.Helper()
	w, out := f.request(t, "POST", "/admin/tenant-templates", map[string]any{"code": code, "name": "只读版"}, "")
	require.Equal(t, 201, w.Code)
	var row model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &row))
	require.JSONEq(t, "[]", string(row.Roles))
	var menus []model.TemplateMenu
	require.NoError(t, json.Unmarshal(row.Menus, &menus))
	require.NotEmpty(t, menus)
	return row
}

func readonlyTemplateRoles() []model.TemplateRole {
	return []model.TemplateRole{
		{Code: "operator", Name: "运营只读", DataScope: "self", Status: "active", Permissions: []string{permission.TradeOrderList, permission.TradeOrderRead}, Menus: []string{"/trade", "/trade/orders"}},
		{Code: "viewer", Name: "只读", DataScope: "self", Status: "active", Permissions: []string{permission.TradeOrderList}, Menus: []string{"/trade", "/trade/orders"}},
	}
}

func putTenantTemplateRoles(t *testing.T, f *tenantFixture, id int64, roles []model.TemplateRole) model.TenantTemplate {
	t.Helper()
	w, out := f.request(t, "PUT", templatePath(id), map[string]any{"name": "只读版", "roles": roles}, "")
	require.Equal(t, 200, w.Code)
	var row model.TenantTemplate
	require.NoError(t, json.Unmarshal(out.Data, &row))
	return row
}

func assertTemplateRoles(t *testing.T, row model.TenantTemplate, want []model.TemplateRole) {
	t.Helper()
	var got []model.TemplateRole
	require.NoError(t, json.Unmarshal(row.Roles, &got))
	require.Equal(t, want, got)
}
