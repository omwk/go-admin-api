package bootstrap

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

func insertS4Audit(t *testing.T, f *systemFixture, tenant int64, row system.AuditRecord) system.AuditRecord {
	t.Helper()
	if len(row.Before) == 0 {
		row.Before = json.RawMessage("{}")
	}
	if len(row.After) == 0 {
		row.After = json.RawMessage("{}")
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Raw(`INSERT INTO audit_log
		(tenant_id,actor_type,actor_id,action,resource_type,resource_id,"before","after",ip,user_agent,trace_id,result,created_at)
		VALUES(?,?,?,?,?,?,?::jsonb,?::jsonb,NULLIF(?,'')::inet,?,?,?,?) RETURNING id`,
		tenant, row.ActorType, row.ActorID, row.Action, row.ResourceType, row.ResourceID, string(row.Before), string(row.After),
		row.IP, row.UserAgent, "s4-integration-trace", row.Result, row.CreatedAt).Scan(&row.ID).Error)
	return row
}

func s4AuditWindow(from, to time.Time) string {
	return "from=" + url.QueryEscape(from.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(to.Format(time.RFC3339Nano))
}

func newS4AuditList(t *testing.T) (*systemFixture, []system.AuditRecord, string) {
	t.Helper()
	f := newSystemFixture(t, nil)
	rows := []system.AuditRecord{}
	for i := range 5 {
		id, action, result, resource := f.actor.User.ID, "system.fixture.a", "success", "user"
		if i%2 == 1 {
			id, action = f.user.ID, "system.fixture.b"
		}
		if i == 1 || i == 2 {
			result = "failure"
		}
		if i == 2 || i == 3 {
			resource = "role"
		}
		rows = append(rows, insertS4Audit(t, f, 1, system.AuditRecord{ActorType: "user", ActorID: &id,
			Action: action, ResourceType: resource, ResourceID: "s4-fixture", Result: result,
			CreatedAt: f.now.Add(time.Duration(i) * time.Second)}))
	}
	return f, rows, s4AuditWindow(rows[0].CreatedAt, rows[4].CreatedAt)
}

func s4AuditPage(t *testing.T, f *systemFixture, query string) system.Page[system.AuditLog] {
	t.Helper()
	response, envelope := systemRequest(t, f, "GET", "/admin/audit-logs?"+query, nil, "")
	require.Equal(t, 200, response.Code)
	var page system.Page[system.AuditLog]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	return page
}
