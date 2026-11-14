package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func userFilterFixture(t *testing.T) (*adminFixture, []model.User) {
	t.Helper()
	f := newAdminFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	nickname, email := "华东代表", "alpha@example.test"
	users := []model.User{
		{Username: "sales01", Nickname: &nickname, Email: &email, Status: "active"},
		{Username: "sales02", Status: "disabled"},
		{Username: "support01", Status: "locked"},
		{Username: "sales-deleted", Nickname: &nickname, Email: &email, Status: "disabled"},
	}
	for i := range users {
		users[i].TenantID = 1
		users[i].DepartmentID = f.user.DepartmentID
		users[i].PasswordHash = f.user.PasswordHash
		users[i].CreatedAt = f.now.Add(time.Duration(i) * time.Minute)
	}
	// F1: release the identifier by soft-deleting its old owner before reuse.
	require.NoError(t, q.Create(&users[3]).Error)
	require.NoError(t, q.Delete(&users[3]).Error)
	require.NoError(t, q.Create(users[:3]).Error)
	foreignDept := model.Department{TenantID: 2, Name: "foreign", Path: "2", Status: "active"}
	require.NoError(t, q.Create(&foreignDept).Error)
	foreign := model.User{TenantID: 2, Username: "sales-foreign", Nickname: &nickname, Email: &email,
		DepartmentID: foreignDept.ID, PasswordHash: f.user.PasswordHash, Status: "disabled"}
	require.NoError(t, q.Create(&foreign).Error)
	return f, users[:3]
}

func userFilterPage(t *testing.T, f *adminFixture, query string) identity.AdminPage[identity.AdminUser] {
	t.Helper()
	response, envelope := adminRequest(t, f, "GET", "/users"+query, nil, "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Zero(t, envelope.Code)
	var page identity.AdminPage[identity.AdminUser]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	return page
}

func TestAdminIntegrationUserFilters(t *testing.T) {
	// BE-1 §4.1–4: filters apply to both total and rows within tenant/deletion scope.
	f, _ := userFilterFixture(t)
	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{"keyword_hit", "keyword=sales01", []string{"sales01"}},
		{"keyword_partial", "keyword=sales", []string{"sales01", "sales02"}},
		{"keyword_case_insensitive", "keyword=SaLeS", []string{"sales01", "sales02"}},
		{"keyword_nickname", "keyword=" + url.QueryEscape("华东"), []string{"sales01"}},
		{"keyword_email", "keyword=ALPHA%40EXAMPLE.TEST", []string{"sales01"}},
		{"keyword_miss", "keyword=missing-user", []string{}},
		{"status_disabled", "status=disabled", []string{"sales02"}},
		{"status_locked", "status=locked", []string{"support01"}},
		{"combined_active", "keyword=sales&status=active", []string{"sales01"}},
		{"combined_disabled", "keyword=sales&status=disabled", []string{"sales02"}},
		{"combined_miss", "keyword=sales&status=locked", []string{}},
		{"keyword_parameterized", "keyword=" + url.QueryEscape("sales01' OR TRUE --"), []string{}},
		{"keyword_256_characters", "keyword=" + url.QueryEscape(strings.Repeat("界", 256)), []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := userFilterPage(t, f, "?"+tc.query)
			names := []string{}
			for _, user := range page.List {
				names = append(names, user.Username)
			}
			require.ElementsMatch(t, tc.want, names)
			require.EqualValues(t, len(tc.want), page.Total)
			t.Logf("%s -> total=%d, users=%v", tc.name, page.Total, names)
		})
	}
}

func TestAdminIntegrationUserFiltersInvalidParameters(t *testing.T) {
	// BE-1 §4.5: invalid filters use the existing 400/10001 response.
	f := newAdminFixture(t)
	for _, tc := range []struct{ name, query string }{
		{"unknown_status", "status=frozen"},
		{"status_case", "status=Active"},
		{"status_parameterized", "status=" + url.QueryEscape("active' OR TRUE --")},
		{"keyword_too_long", "keyword=" + strings.Repeat("x", 257)},
		{"keyword_unicode_too_long", "keyword=" + url.QueryEscape(strings.Repeat("界", 257))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, envelope := adminRequest(t, f, "GET", "/users?"+tc.query, nil, "")
			require.Equal(t, 400, response.Code, response.Body.String())
			require.Equal(t, 10001, envelope.Code)
			t.Logf("%s -> HTTP %d / code %d", tc.name, response.Code, envelope.Code)
		})
	}
}

func TestAdminIntegrationUserFiltersPaginationAndDefaults(t *testing.T) {
	// BE-1 §4.6–7: filters preserve the size cap, defaults and sort whitelist.
	f, users := userFilterFixture(t)
	t.Run("no_parameters_and_empty_filters", func(t *testing.T) {
		page := userFilterPage(t, f, "")
		require.Equal(t, 1, page.Page)
		require.Equal(t, 20, page.PageSize)
		require.EqualValues(t, 5, page.Total) // Seeded admin/operator plus three visible fixtures.
		require.Len(t, page.List, 5)
		require.Equal(t, []int64{users[2].ID, users[1].ID, users[0].ID}, []int64{page.List[0].ID, page.List[1].ID, page.List[2].ID})
		require.Equal(t, page, userFilterPage(t, f, "?page=1&page_size=20&sort=-created_at"))
		require.Equal(t, page, userFilterPage(t, f, "?keyword=&status="))
		t.Logf("no parameters / empty filters -> total=%d, page=%d, page_size=%d, newest first", page.Total, page.Page, page.PageSize)
	})
	t.Run("sorts", func(t *testing.T) {
		for _, tc := range []struct {
			sort string
			ids  []int64
		}{
			{"-created_at", []int64{users[1].ID, users[0].ID}},
			{"created_at", []int64{users[0].ID, users[1].ID}},
			{"username", []int64{users[0].ID, users[1].ID}},
			{"-username", []int64{users[1].ID, users[0].ID}},
		} {
			page := userFilterPage(t, f, "?keyword=sales&sort="+tc.sort)
			require.Len(t, page.List, 2)
			require.Equal(t, tc.ids, []int64{page.List[0].ID, page.List[1].ID})
		}
	})
	t.Run("page_size_cap_and_filtered_pagination", func(t *testing.T) {
		q, cancel := f.database.WithContext(f.ctx)
		defer cancel()
		rows := make([]model.User, 101)
		for i := range rows {
			rows[i] = model.User{TenantID: 1, Username: fmt.Sprintf("paging%03d", i), DepartmentID: f.user.DepartmentID,
				PasswordHash: f.user.PasswordHash, Status: "active"}
		}
		require.NoError(t, q.Create(&rows).Error)
		page := userFilterPage(t, f, "?keyword=paging&status=active&page_size=200&sort=username")
		require.Equal(t, 100, page.PageSize)
		require.EqualValues(t, 101, page.Total)
		require.Len(t, page.List, 100)
		require.Equal(t, rows[0].ID, page.List[0].ID)
		t.Logf("page_size=200 -> page_size=%d, rows=%d, total=%d", page.PageSize, len(page.List), page.Total)
		page = userFilterPage(t, f, "?keyword=paging&status=active&page_size=200&page=2&sort=username")
		require.Equal(t, 2, page.Page)
		require.Equal(t, 100, page.PageSize)
		require.EqualValues(t, 101, page.Total)
		require.Len(t, page.List, 1)
		require.Equal(t, rows[100].ID, page.List[0].ID)
	})
}

func TestAdminIntegrationUserFiltersPreserveScope(t *testing.T) {
	// BE-1 §3: OR predicates on all three columns cannot bypass row visibility.
	f, _ := userFilterFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	ordinaryScopeActor(t, f.identityFixture, f.actor.User.ID)
	require.NoError(t, q.Model(&model.Role{}).Where("tenant_id = ? AND code = ?", 1, identity.SuperAdminRoleCode).Update("data_scope", "self").Error)
	for _, query := range []string{"keyword=sales", "keyword=" + url.QueryEscape("华东"), "keyword=alpha", "keyword=sales&status=active", "status=disabled"} {
		page := userFilterPage(t, f, "?"+query)
		require.Zero(t, page.Total)
		require.Empty(t, page.List)
	}
	page := userFilterPage(t, f, "?keyword=admin&status=active")
	require.EqualValues(t, 1, page.Total)
	require.Len(t, page.List, 1)
	require.Equal(t, f.actor.User.ID, page.List[0].ID)
	t.Log("self scope excludes matching usernames, nicknames, emails and statuses outside the actor")
}
