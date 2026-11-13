package bootstrap

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

func departmentBody(name string, parent int64) map[string]any {
	return map[string]any{"parent_id": parent, "name": name, "sort": 0, "status": "active"}
}

func createS1Department(t *testing.T, f *adminFixture, name string, parent int64) repo.DepartmentRow {
	t.Helper()
	response, envelope := adminRequest(t, f, "POST", "/depts", departmentBody(name, parent), "")
	require.Equal(t, 201, response.Code, response.Body.String())
	var row repo.DepartmentRow
	require.NoError(t, json.Unmarshal(envelope.Data, &row))
	return row
}

func readS1Department(t *testing.T, f *adminFixture, id int64) repo.DepartmentRow {
	t.Helper()
	var row repo.DepartmentRow
	require.NoError(t, json.Unmarshal(f2AdminGet(t, f, resourcePath("depts", id), 200), &row))
	return row
}

func flattenS1Departments(nodes []*identity.AdminDepartment) map[int64]repo.DepartmentRow {
	rows := map[int64]repo.DepartmentRow{}
	queue := append([]*identity.AdminDepartment{}, nodes...)
	for len(queue) > 0 {
		node := queue[0]
		queue = append(queue[1:], node.Children...)
		rows[node.ID] = node.DepartmentRow
	}
	return rows
}

func TestAdminS1IntegrationDepartmentCreateTree(t *testing.T) {
	// S1 §4.1/2/11: writes and GET tree/detail all validate against identity.yaml.
	f := newAdminFixture(t)
	root := readS1Department(t, f, f.user.DepartmentID)
	first := createS1Department(t, f, "S1 first", root.ID)
	second := createS1Department(t, f, "S1 second", first.ID)
	sibling := createS1Department(t, f, "S1 sibling", root.ID)
	require.Equal(t, root.Path+"."+strconv.FormatInt(first.ID, 10), first.Path)
	require.Equal(t, first.Path+"."+strconv.FormatInt(second.ID, 10), second.Path)
	require.Equal(t, root.Path+"."+strconv.FormatInt(sibling.ID, 10), sibling.Path)
	var tree []*identity.AdminDepartment
	require.NoError(t, json.Unmarshal(f2AdminGet(t, f, "/depts", 200), &tree))
	rows := flattenS1Departments(tree)
	for _, row := range []repo.DepartmentRow{first, second, sibling} {
		require.Equal(t, row, rows[row.ID])
	}
	// A missing sort uses zero; multibyte names use character rather than byte limits.
	body := departmentBody(strings.Repeat("部", 64), root.ID)
	delete(body, "sort")
	response, _ := adminRequest(t, f, "POST", "/depts", body, "")
	require.Equal(t, 201, response.Code, response.Body.String())
}

func TestAdminS1IntegrationDepartmentDuplicateAndValidation(t *testing.T) {
	// S1 §4.3: both creation and rename/reparent reject sibling duplicates as 400/10001.
	f := newAdminFixture(t)
	parent := createS1Department(t, f, "S1 names", f.user.DepartmentID)
	one := createS1Department(t, f, "same", parent.ID)
	two := createS1Department(t, f, "different", parent.ID)
	other := createS1Department(t, f, "same", f.user.DepartmentID)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/depts"}, {"PUT", resourcePath("depts", two.ID)}, {"PUT", resourcePath("depts", other.ID)},
	} {
		response, envelope := adminRequest(t, f, tc.method, tc.path, departmentBody(one.Name, parent.ID), "")
		require.Equal(t, 400, response.Code, response.Body.String())
		require.Equal(t, 10001, envelope.Code)
	}
	response, _ := adminRequest(t, f, "PUT", resourcePath("depts", one.ID), departmentBody(one.Name, parent.ID), "")
	require.Equal(t, 200, response.Code)
	for _, tc := range []struct {
		field string
		value any
	}{
		{"name", ""}, {"name", strings.Repeat("部", 65)}, {"parent_id", -1},
		{"status", "locked"}, {"status", ""}, {"path", "client.path"},
		{"tenant_id", 2}, {"sort", 1.5}, {"sort", int64(2147483648)},
	} {
		body := departmentBody("invalid", parent.ID)
		body[tc.field] = tc.value
		for _, operation := range []struct{ method, path string }{{"POST", "/depts"}, {"PUT", resourcePath("depts", two.ID)}} {
			response, envelope := adminRequest(t, f, operation.method, operation.path, body, "")
			require.Equal(t, 400, response.Code, response.Body.String())
			require.Equal(t, 10001, envelope.Code)
		}
	}
}

func TestAdminS1IntegrationDepartmentCyclesAndSubtreeMove(t *testing.T) {
	// S1 §4.4/5: two descendant levels move together; self/descendant parents are rejected.
	f := newAdminFixture(t)
	a := createS1Department(t, f, "S1 a", f.user.DepartmentID)
	b := createS1Department(t, f, "S1 b", a.ID)
	c := createS1Department(t, f, "S1 c", b.ID)
	target := createS1Department(t, f, "S1 target", f.user.DepartmentID)
	for _, parent := range []int64{a.ID, b.ID, c.ID} {
		response, envelope := adminRequest(t, f, "PUT", resourcePath("depts", a.ID), departmentBody(a.Name, parent), "")
		require.Equal(t, 400, response.Code, response.Body.String())
		require.Equal(t, 10001, envelope.Code)
		require.Equal(t, a, readS1Department(t, f, a.ID))
	}
	body := departmentBody("S1 moved", target.ID)
	body["sort"], body["status"] = -3, "disabled"
	response, _ := adminRequest(t, f, "PUT", resourcePath("depts", a.ID), body, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	base := target.Path + "." + strconv.FormatInt(a.ID, 10)
	for _, row := range []repo.DepartmentRow{a, b, c} {
		current := readS1Department(t, f, row.ID)
		require.Equal(t, base+strings.TrimPrefix(row.Path, a.Path), current.Path)
	}
	current := readS1Department(t, f, a.ID)
	require.Equal(t, target.ID, current.ParentID)
	require.Equal(t, "S1 moved", current.Name)
	require.Equal(t, -3, current.Sort)
	require.Equal(t, "disabled", current.Status)
	// Moving back under the seeded root recomputes the same complete subtree.
	response, _ = adminRequest(t, f, "PUT", resourcePath("depts", a.ID), departmentBody(a.Name, a.ParentID), "")
	require.Equal(t, 200, response.Code)
	for _, row := range []repo.DepartmentRow{a, b, c} {
		require.Equal(t, row.Path, readS1Department(t, f, row.ID).Path)
	}
}

func TestAdminS1IntegrationDepartmentDeleteGuardsAndAudit(t *testing.T) {
	// S1 §4.6/7: children/users/root protection; disabled empty departments can be hard deleted.
	f := newAdminFixture(t)
	parent := createS1Department(t, f, "S1 protected", f.user.DepartmentID)
	child := createS1Department(t, f, "S1 empty", parent.ID)
	memberDept := createS1Department(t, f, "S1 members", f.user.DepartmentID)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Create(&model.User{TenantID: 1, Username: "s1-member", PasswordHash: f.user.PasswordHash, DepartmentID: memberDept.ID, Status: "disabled"}).Error)
	for _, tc := range []struct {
		id     int64
		status int
	}{{parent.ID, 409}, {memberDept.ID, 409}, {f.user.DepartmentID, 400}} {
		response, _ := adminRequest(t, f, "DELETE", resourcePath("depts", tc.id), nil, "")
		require.Equal(t, tc.status, response.Code, response.Body.String())
	}
	body := departmentBody(child.Name, child.ParentID)
	body["status"] = "disabled"
	response, _ := adminRequest(t, f, "PUT", resourcePath("depts", child.ID), body, "")
	require.Equal(t, 200, response.Code)
	response, _ = adminRequest(t, f, "DELETE", resourcePath("depts", child.ID), nil, "")
	require.Equal(t, 204, response.Code, response.Body.String())
	f2AdminGet(t, f, resourcePath("depts", child.ID), 404)
	var tree []*identity.AdminDepartment
	require.NoError(t, json.Unmarshal(f2AdminGet(t, f, "/depts", 200), &tree))
	require.NotContains(t, flattenS1Departments(tree), child.ID)
	var count int64
	require.NoError(t, q.Table("sys_dept").Where("id = ?", child.ID).Count(&count).Error)
	require.Zero(t, count)
	var record struct{ Before, After json.RawMessage }
	require.NoError(t, q.Table("audit_log").Select("\"before\", \"after\"").Where("tenant_id = ? AND action = ? AND resource_id = ?", 1, "system:dept:delete", strconv.FormatInt(child.ID, 10)).Take(&record).Error)
	var before map[string]any
	require.NoError(t, json.Unmarshal(record.Before, &before))
	require.Equal(t, map[string]any{"id": float64(child.ID), "tenant_id": float64(1), "parent_id": float64(parent.ID), "name": child.Name, "path": child.Path, "sort": float64(0), "status": "disabled"}, before)
	require.Equal(t, "null", string(record.After))
	// Legacy locked departments are deletable too; status never acts as a deletion guard.
	locked := model.Department{TenantID: 1, ParentID: parent.ID, Name: "S1 locked", Path: parent.Path + ".locked", Status: "locked"}
	require.NoError(t, q.Create(&locked).Error)
	response, _ = adminRequest(t, f, "DELETE", resourcePath("depts", locked.ID), nil, "")
	require.Equal(t, 204, response.Code, response.Body.String())
}

func TestAdminS1IntegrationDepartmentTenantAndPermission(t *testing.T) {
	// S1 §4.8/9: foreign parents/targets and absent targets are 404, absent permissions 403/20107.
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	foreign := model.Department{TenantID: 2, Name: "S1 foreign", Path: "foreign", Status: "active"}
	require.NoError(t, q.Create(&foreign).Error)
	local := createS1Department(t, f, "S1 local", f.user.DepartmentID)
	for _, id := range []int64{foreign.ID, 99999999} {
		for _, tc := range []struct {
			method, path string
			body         any
		}{
			{"POST", "/depts", departmentBody("x", id)},
			{"PUT", resourcePath("depts", local.ID), departmentBody("x", id)},
			{"PUT", resourcePath("depts", id), departmentBody("x", local.ParentID)},
			{"DELETE", resourcePath("depts", id), nil},
		} {
			response, envelope := adminRequest(t, f, tc.method, tc.path, tc.body, "")
			require.Equal(t, 404, response.Code, response.Body.String())
			require.Equal(t, 10003, envelope.Code)
		}
	}
	assignProfileRole(t, f.identityFixture, identity.OperatorRoleCode)
	f.bearer = f.login(t).AccessToken
	for _, tc := range []struct{ method, path string }{{"POST", "/depts"}, {"PUT", resourcePath("depts", local.ID)}, {"DELETE", resourcePath("depts", local.ID)}} {
		var body any
		if tc.method != "DELETE" {
			body = departmentBody("x", local.ParentID)
		}
		response, envelope := adminRequest(t, f, tc.method, tc.path, body, "")
		require.Equal(t, 403, response.Code, response.Body.String())
		require.Equal(t, 20107, envelope.Code)
	}
}

func TestAdminS1IntegrationDepartmentIdempotency(t *testing.T) {
	// S1 §4.10: all three writes replay committed results; body mismatches remain 409/10006.
	f := newAdminFixture(t)
	body := departmentBody("S1 replay", f.user.DepartmentID)
	first, saved := adminRequest(t, f, "POST", "/depts", body, "s1-create")
	require.Equal(t, 201, first.Code, first.Body.String())
	second, replayed := adminRequest(t, f, "POST", "/depts", body, "s1-create")
	require.Equal(t, 201, second.Code)
	require.JSONEq(t, string(saved.Data), string(replayed.Data))
	body["name"] = "different body"
	response, envelope := adminRequest(t, f, "POST", "/depts", body, "s1-create")
	require.Equal(t, 409, response.Code)
	require.Equal(t, 10006, envelope.Code)
	var row repo.DepartmentRow
	require.NoError(t, json.Unmarshal(saved.Data, &row))
	path := resourcePath("depts", row.ID)
	for _, method := range []string{"PUT", "DELETE"} {
		var payload any
		if method == "PUT" {
			payload = body
		}
		first, saved = adminRequest(t, f, method, path, payload, "s1-"+method)
		second, replayed = adminRequest(t, f, method, path, payload, "s1-"+method)
		status := 204
		if method == "PUT" {
			status = 200
			require.JSONEq(t, string(saved.Data), string(replayed.Data))
		}
		require.Equal(t, status, first.Code, first.Body.String())
		require.Equal(t, status, second.Code, second.Body.String())
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("audit_log").Where("action = ? AND resource_id = ?", "system:dept:create", strconv.FormatInt(row.ID, 10)).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
