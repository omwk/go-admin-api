package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func f2AdminGet(t *testing.T, f *adminFixture, path string, status int) json.RawMessage {
	t.Helper()
	response, envelope := shopHTTPRequest(t, f.router, "GET", "/api/v1/admin"+path, nil, f.bearer)
	require.Equal(t, status, response.Code, response.Body.String())
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		parts[2] = "{id}"
	}
	validateOpenAPIResponse(t, f.contract, "/api/v1/admin"+strings.Join(parts, "/"), "get", status, response.Body.Bytes())
	return envelope.Data
}

func TestAdminF2IntegrationDepartmentTreeAndUser(t *testing.T) {
	// F2 §4.1/2: a three-level tree includes empty departments and safe account details.
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	parent := int64(0)
	ids := []int64{}
	for _, name := range []string{"F2 root", "F2 child", "F2 leaf"} {
		row := model.Department{TenantID: 1, ParentID: parent, Name: name, Path: "f2", Status: "active"}
		require.NoError(t, q.Create(&row).Error)
		ids = append(ids, row.ID)
		parent = row.ID
	}
	var tree []struct {
		ID       int64 `json:"id"`
		Children []struct {
			ID       int64 `json:"id"`
			Children []struct {
				ID          int64 `json:"id"`
				MemberCount int64 `json:"member_count"`
			} `json:"children"`
		} `json:"children"`
	}
	require.NoError(t, json.Unmarshal(f2AdminGet(t, f, "/depts", 200), &tree))
	found := false
	for _, root := range tree {
		if root.ID == ids[0] {
			found = true
			require.Len(t, root.Children, 1)
			require.Equal(t, ids[1], root.Children[0].ID)
			require.Len(t, root.Children[0].Children, 1)
			require.Equal(t, ids[2], root.Children[0].Children[0].ID)
			require.Zero(t, root.Children[0].Children[0].MemberCount)
		}
	}
	require.True(t, found)
	var detail map[string]any
	require.NoError(t, json.Unmarshal(f2AdminGet(t, f, resourcePath("users", f.user.ID), 200), &detail))
	for _, field := range []string{"dept_id", "status", "created_at", "updated_at"} {
		require.Contains(t, detail, field)
	}
	for _, field := range []string{"password_hash", "phone", "failed_attempts", "locked_until"} {
		require.NotContains(t, detail, field)
	}
	f2AdminGet(t, f, "/users/99999999", 404)
	f2AdminGet(t, f, resourcePath("depts", ids[2]), 200)
}

func TestAdminF2IntegrationAssociationReads(t *testing.T) {
	// F2 §4.3: authorization edit forms read exactly the stored IDs.
	f := newAdminFixture(t)
	role := createAdminRole(t, f, "f2-readback")
	ids := []int64{}
	for _, name := range []string{"f2-one", "f2-two", "f2-three"} {
		ids = append(ids, createAdminMenu(t, f, name, 0).ID)
	}
	path := resourcePath("roles", role.ID)
	response, _ := adminRequest(t, f, "PUT", path+"/menus", map[string]any{"menu_ids": ids}, "")
	require.Equal(t, 204, response.Code)
	var got []int64
	require.NoError(t, json.Unmarshal(f2AdminGet(t, f, path+"/menus", 200), &got))
	require.Equal(t, ids, got)
	for _, suffix := range []string{"", "/permissions", "/depts"} {
		f2AdminGet(t, f, path+suffix, 200)
	}
	f2AdminGet(t, f, resourcePath("menus", ids[0]), 200)
	f2AdminGet(t, f, resourcePath("users", f.user.ID)+"/roles", 200)
}

func TestAdminF2IntegrationCrossTenantReadsAreNotFound(t *testing.T) {
	// F2 §4.4: a valid foreign identifier is indistinguishable from a missing one.
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	dept := model.Department{TenantID: 2, Name: "f2 foreign", Path: "f2.foreign", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := model.User{TenantID: 2, Username: "f2-foreign", PasswordHash: f.user.PasswordHash, DepartmentID: dept.ID, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	role := model.Role{TenantID: 2, Code: "f2-foreign", Name: "foreign", DataScope: "all", Status: "active"}
	require.NoError(t, q.Create(&role).Error)
	menu := model.Menu{TenantID: 2, Name: "f2 foreign", Type: "menu", Status: "active"}
	require.NoError(t, q.Create(&menu).Error)
	for _, path := range []string{
		resourcePath("depts", dept.ID), resourcePath("users", user.ID), resourcePath("users", user.ID) + "/roles",
		resourcePath("roles", role.ID), resourcePath("roles", role.ID) + "/menus", resourcePath("roles", role.ID) + "/permissions",
		resourcePath("roles", role.ID) + "/depts", resourcePath("menus", menu.ID),
	} {
		f2AdminGet(t, f, path, 404)
	}
}
