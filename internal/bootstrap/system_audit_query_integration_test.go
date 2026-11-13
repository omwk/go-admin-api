package bootstrap

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func TestSystemAuditIntegrationListFiltersAndTenant(t *testing.T) {
	// S4 `5.1/2: real PostgreSQL filtering, ordering and tenant isolation.
	f, rows, window := newS4AuditList(t)
	require.NoError(t, f.database.Exec(f.ctx, "INSERT INTO tenant(id,code,name) OVERRIDING SYSTEM VALUE VALUES(2,?,?)", "s4-other", "Other tenant"))
	insertS4Audit(t, f, 2, rows[4])
	page := s4AuditPage(t, f, window+"&tenant_id=2")
	require.EqualValues(t, 5, page.Total)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 20, page.PageSize)
	require.Len(t, page.List, 5)
	for i, row := range page.List {
		require.Equal(t, rows[4-i].ID, row.ID)
	}
	actor := strconv.FormatInt(f.actor.User.ID, 10)
	for _, tc := range []struct {
		query string
		count int
	}{
		{"action=system.fixture.a", 3}, {"result=failure", 2}, {"actor_id=" + actor, 3},
		{"action=system.fixture.a&result=failure&actor_id=" + actor, 1},
		{"resource_type=role", 2},
		{"action=" + url.QueryEscape("' OR 1=1 --"), 0},
	} {
		page := s4AuditPage(t, f, window+"&"+tc.query)
		require.EqualValues(t, tc.count, page.Total, tc.query)
		require.Len(t, page.List, tc.count)
	}
	t.Log("S4 #1/#2: five descending rows; action/result/actor_id single and combined filters; tenant isolation PASS")
}

func TestSystemAuditIntegrationInclusiveRangeAndPagination(t *testing.T) {
	// S4 `5.3/4: exact timestamp boundaries and strict pagination validation.
	f, rows, window := newS4AuditList(t)
	page := s4AuditPage(t, f, s4AuditWindow(rows[2].CreatedAt, rows[2].CreatedAt))
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, rows[2].ID, page.List[0].ID)
	page = s4AuditPage(t, f, window+"&page=2&page_size=2")
	require.EqualValues(t, 5, page.Total)
	require.Equal(t, 2, page.Page)
	require.Equal(t, 2, page.PageSize)
	require.Equal(t, []int64{rows[2].ID, rows[1].ID}, []int64{page.List[0].ID, page.List[1].ID})
	page = s4AuditPage(t, f, window+"&page_size=100")
	require.Equal(t, 100, page.PageSize)
	page = s4AuditPage(t, f, window+"&page=4&page_size=2")
	require.Empty(t, page.List)
	for _, query := range []string{
		s4AuditWindow(rows[4].CreatedAt, rows[0].CreatedAt), "from=invalid", "to=invalid",
		"page_size=101", "page_size=0", "page_size=-1", "page_size=abc",
		"page=0", "page=-1", "page=10001", "page=99999999999999999999",
		"actor_id=-1", "actor_id=0", "actor_id=abc", "result=invalid",
	} {
		response, envelope := systemRequest(t, f, "GET", "/admin/audit-logs?"+query, nil, "")
		require.Equal(t, 400, response.Code, query)
		require.Equal(t, 10001, envelope.Code)
	}
	t.Log("S4 #3/#4: inclusive identical boundaries=1 row; reversed range=400; page_size=100 accepted, 101 rejected")
}

func TestSystemAuditIntegrationRedactionAndTruncation(t *testing.T) {
	// S4 `5.5/6: exercise JSONB through HTTP, including nested sensitive keys.
	f := newSystemFixture(t, nil)
	private, err := refresh.Generate()
	require.NoError(t, err)
	state, err := json.Marshal(map[string]any{
		"password_hash": private, "cipher": private, "nonce": private,
		"nested": []any{map[string]string{"secret": private, "token": private}},
	})
	require.NoError(t, err)
	insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "system", Action: "s4.redaction", ResourceType: "fixture",
		ResourceID: "redacted", Result: "success", Before: state, After: state, CreatedAt: f.now})
	response, envelope := systemRequest(t, f, "GET", "/admin/audit-logs?action=s4.redaction", nil, "")
	require.Equal(t, 200, response.Code)
	require.NotContains(t, response.Body.String(), private)
	var page system.Page[system.AuditLog]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	require.Len(t, page.List, 1)
	for _, raw := range []json.RawMessage{page.List[0].Before, page.List[0].After} {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		for _, key := range []string{"password_hash", "cipher", "nonce"} {
			require.Equal(t, "***", decoded[key])
		}
		nested := decoded["nested"].([]any)[0].(map[string]any)
		require.Equal(t, "***", nested["secret"])
		require.Equal(t, "***", nested["token"])
	}
	large, err := json.Marshal(map[string]string{"text": strings.Repeat("审计<&>", 3000), "secret": private})
	require.NoError(t, err)
	insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "system", Action: "s4.truncation", ResourceType: "fixture",
		ResourceID: "truncated", Result: "success", Before: large, After: large, CreatedAt: f.now})
	page = s4AuditPage(t, f, "action=s4.truncation")
	require.Len(t, page.List, 1)
	require.True(t, page.List[0].Truncated)
	require.LessOrEqual(t, len(page.List[0].Before)+len(page.List[0].After), 4096)
	require.True(t, json.Valid(page.List[0].Before))
	require.True(t, json.Valid(page.List[0].After))
	require.NotContains(t, string(page.List[0].Before)+string(page.List[0].After), private)
	t.Log("S4 #5/#6: recursive masking=***; sanitized combined before/after <=4096 bytes and truncated=true")
}
