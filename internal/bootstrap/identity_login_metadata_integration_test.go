package bootstrap

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/modules/system"
	"github.com/stretchr/testify/require"
)

type loginMetadataCase struct {
	name, userAgent, wantUserAgent string
	status, code                   int
	wrong, unknown, limited, down  bool
}

func TestIdentityLoginMetadataIntegrationHTTP(t *testing.T) {
	// FOLLOWUP-S4 §4 / REQ-IAM-001/009: real HTTP attempts persist bounded source metadata.
	for _, tc := range []loginMetadataCase{
		{name: "success", userAgent: "S4 success client", wantUserAgent: "S4 success client", status: 200},
		{name: "wrong_password", userAgent: "S4 failure client", wantUserAgent: "S4 failure client", status: 401, code: 20101, wrong: true},
		{name: "unknown_account", userAgent: "S4 unknown client", wantUserAgent: "S4 unknown client", status: 401, code: 20101, unknown: true},
		{name: "rate_limited", userAgent: "S4 limited client", wantUserAgent: "S4 limited client", status: 429, code: 10005, limited: true},
		{name: "long_user_agent", userAgent: strings.Repeat("a", 4000), wantUserAgent: strings.Repeat("a", 256), status: 200},
		{name: "multibyte_user_agent", userAgent: strings.Repeat("a", 255) + "界" + strings.Repeat("b", 3742), wantUserAgent: strings.Repeat("a", 255), status: 200},
		{name: "exact_boundary_user_agent", userAgent: strings.Repeat("a", 253) + "界", wantUserAgent: strings.Repeat("a", 253) + "界", status: 200},
		{name: "redis_unavailable", userAgent: "S4 dependency client", wantUserAgent: "S4 dependency client", status: 503, code: 10002, down: true},
	} {
		t.Run(tc.name, func(t *testing.T) { assertHTTPLoginMetadata(t, tc) })
	}
}

func assertHTTPLoginMetadata(t *testing.T, tc loginMetadataCase) {
	t.Helper()
	f := newSystemFixture(t, nil)
	username, password, unknown := f.user.Username, f.password, ""
	if tc.wrong || tc.unknown {
		password = accountPassword(t)
	}
	if tc.unknown {
		unknown = accountPassword(t)
		username = unknown
	}
	if tc.limited {
		for range 10 {
			allowed, err := f.redis.AllowLogin(f.ctx, "127.0.0.1")
			require.NoError(t, err)
			require.True(t, allowed)
		}
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var previousID int64
	require.NoError(t, q.Table("audit_log").Select("COALESCE(MAX(id),0)").Scan(&previousID).Error)
	if tc.down {
		f.server.SetError("ERR dependency unavailable")
	}
	server := httptest.NewServer(identityRouter(t, f.identityFixture))
	t.Cleanup(server.Close)
	status, envelope := loginMetadataHTTP(t, f, server, username, password, tc.userAgent)
	require.Equal(t, tc.status, status)
	require.Equal(t, tc.code, envelope.Code)
	f.server.SetError("")
	var rows []system.AuditRecord
	require.NoError(t, q.Table("audit_log").Select(`id,actor_id,resource_id,result,COALESCE(host(ip),'') AS ip,user_agent`).
		Where("tenant_id=1 AND action='identity.login' AND id>?", previousID).Find(&rows).Error)
	require.Len(t, rows, 1, "each HTTP attempt must append exactly one login audit")
	row := rows[0]
	wantResult := "failure"
	if tc.status == http.StatusOK {
		wantResult = "success"
	}
	require.Equal(t, wantResult, row.Result)
	require.Equal(t, "127.0.0.1", row.IP)
	require.Equal(t, tc.wantUserAgent, row.UserAgent)
	require.LessOrEqual(t, len(row.UserAgent), 256)
	require.True(t, utf8.ValidString(row.UserAgent))
	if tc.unknown || tc.limited || tc.down {
		require.Equal(t, "unknown", row.ResourceID)
		require.Nil(t, row.ActorID)
	} else {
		require.Equal(t, strconv.FormatInt(f.user.ID, 10), row.ResourceID)
		require.Equal(t, &f.user.ID, row.ActorID)
	}
	assertLoginMetadataVisibleAndSafe(t, f, row, []string{f.password, password, unknown, envelope.Data.AccessToken, envelope.Data.RefreshToken})
	t.Logf("FOLLOWUP-S4: HTTP %d; exactly one audit; IP/UA match (%d -> %d bytes); login-logs visible; no credential leak", status, len(tc.userAgent), len(row.UserAgent))
}

func loginMetadataHTTP(t *testing.T, f *systemFixture, server *httptest.Server, username, password, userAgent string) (int, authEnvelope) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, server.URL+"/api/v1/auth/login", bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var envelope authEnvelope
	require.NoError(t, json.Unmarshal(raw, &envelope))
	return response.StatusCode, envelope
}

func assertLoginMetadataVisibleAndSafe(t *testing.T, f *systemFixture, row system.AuditRecord, sensitive []string) {
	t.Helper()
	response, envelope := systemRequest(t, f, http.MethodGet, "/admin/login-logs", nil, "")
	require.Equal(t, http.StatusOK, response.Code)
	var page system.Page[system.LoginLog]
	require.NoError(t, json.Unmarshal(envelope.Data, &page))
	visible := map[int64]system.LoginLog{}
	for _, item := range page.List {
		visible[item.ID] = item
	}
	require.Contains(t, visible, row.ID)
	item := visible[row.ID]
	require.Equal(t, row.IP, item.IP)
	require.Equal(t, row.UserAgent, item.UserAgent)
	require.Equal(t, row.ResourceID, item.ResourceID)
	require.Equal(t, row.Result, item.Result)
	require.Equal(t, row.ActorID, item.UserID)
	if row.ResourceID == "unknown" {
		require.Equal(t, "未知账号", item.Username)
	} else {
		require.Equal(t, f.user.Username, item.Username)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var stored string
	require.NoError(t, q.Raw("SELECT to_jsonb(audit_log)::text FROM audit_log WHERE id=?", row.ID).Scan(&stored).Error)
	for _, value := range sensitive {
		if value != "" {
			require.False(t, strings.Contains(stored, value), "persisted audit must not contain credentials or unknown input")
			require.False(t, strings.Contains(response.Body.String(), value), "login history must not expose credentials or unknown input")
		}
	}
}
