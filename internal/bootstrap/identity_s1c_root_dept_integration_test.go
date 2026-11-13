package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func TestAdminS1cIntegrationDepartmentCreateRootRejected(t *testing.T) {
	// S1c §4.1/2: root rejection precedes duplicate names and never inserts a row.
	f := newAdminFixture(t)
	root := readS1Department(t, f, f.user.DepartmentID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, tc := range []struct{ scenario, name string }{
		{"new_name", "S1c rejected root"}, {"existing_root_name", root.Name},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			var before, after int64
			require.NoError(t, q.Table("sys_dept").Count(&before).Error)
			response, envelope := adminRequest(t, f, "POST", "/depts", departmentBody(tc.name, 0), "")
			require.Equal(t, 400, response.Code, response.Body.String())
			require.Equal(t, 10001, envelope.Code)
			var failure struct{ Message string }
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
			require.Equal(t, "已存在根部门，请选择上级部门", failure.Message)
			require.NoError(t, q.Table("sys_dept").Count(&after).Error)
			require.Equal(t, before, after)
			t.Logf("POST parent_id=0: HTTP %d, %s; sys_dept rows %d -> %d", response.Code, failure.Message, before, after)
		})
	}
}

func TestAdminS1cIntegrationDepartmentMoveToRootRejected(t *testing.T) {
	// S1c §4.3: a normal department cannot become a root, even with the root's name.
	f := newAdminFixture(t)
	root := readS1Department(t, f, f.user.DepartmentID)
	row := createS1Department(t, f, "S1c department", root.ID)
	child := createS1Department(t, f, "S1c child", row.ID)
	for _, name := range []string{row.Name, root.Name} {
		response, envelope := adminRequest(t, f, "PUT", resourcePath("depts", row.ID), departmentBody(name, 0), "")
		require.Equal(t, 400, response.Code, response.Body.String())
		require.Equal(t, 10001, envelope.Code)
		var failure struct{ Message string }
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
		require.Equal(t, "根部门不可移动", failure.Message)
		require.Equal(t, row, readS1Department(t, f, row.ID))
		require.Equal(t, child, readS1Department(t, f, child.ID))
		t.Logf("PUT parent_id=0: HTTP %d, %s; department and child unchanged", response.Code, failure.Message)
	}
}

func TestAdminS1cIntegrationDepartmentRootRenamePreservesPath(t *testing.T) {
	// S1c §4.4: the seeded root may change name/sort while keeping its path and children.
	f := newAdminFixture(t)
	root := readS1Department(t, f, f.user.DepartmentID)
	require.Equal(t, "1", root.Path)
	child := createS1Department(t, f, "S1c first level", root.ID)
	body := departmentBody("S1c renamed root", 0)
	body["sort"] = -3
	response, envelope := adminRequest(t, f, "PUT", resourcePath("depts", root.ID), body, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Zero(t, envelope.Code)
	var updated repo.DepartmentRow
	require.NoError(t, json.Unmarshal(envelope.Data, &updated))
	root.Name, root.Sort = "S1c renamed root", -3
	require.Equal(t, root, updated)
	require.Equal(t, root, readS1Department(t, f, root.ID))
	require.Equal(t, child, readS1Department(t, f, child.ID))
	t.Logf("root rename/sort: HTTP %d, path=%s; child path=%s unchanged", response.Code, updated.Path, child.Path)
}
