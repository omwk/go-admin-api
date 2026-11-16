package bootstrap

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/stretchr/testify/require"
)

func TestIdentityLogoutOthersIntegrationKeepsCurrentCountsAndIsolatesUsers(t *testing.T) {
	// BE-IDN-005 §3 #1-3/6/7: three devices, rotation history and a tenant peer.
	f := newAccountFixture(t)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET is_super=false WHERE id=?", f.user.ID))
	require.NoError(t, f.database.Exec(f.ctx, "DELETE FROM sys_user_role WHERE user_id=?", f.user.ID))
	first, second := f.login(t), f.login(t)
	rotated, err := f.service.Refresh(f.ctx, first.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	peer := logoutOthersPeer(t, f)
	secrets := []identity.Tokens{first, second, rotated, peer}
	claims, err := f.service.Authenticate(f.ctx, f.token)
	require.NoError(t, err)
	require.Equal(t, 401, f.request(t, "POST", "/auth/logout-others", "", nil).Code)
	response := f.request(t, "POST", "/auth/logout-others", f.token, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	validateOpenAPIResponse(t, readIdentityOpenAPI(t), "/api/v1/auth/logout-others", "post", response.Code, response.Body.Bytes())
	var result map[string]int
	notificationDataF3(t, response, &result)
	require.Equal(t, map[string]int{"revoked": 2}, result)
	require.Equal(t, 200, f.request(t, "GET", "/auth/sessions", f.token, nil).Code)
	for _, tokens := range []identity.Tokens{first, second, rotated} {
		denied := f.request(t, "GET", "/auth/sessions", tokens.AccessToken, nil)
		require.Equal(t, 401, denied.Code, denied.Body.String())
		_, err = f.service.Refresh(f.ctx, tokens.RefreshToken, identity.RequestMeta{})
		require.Equal(t, 20103, errs.From(err).Code)
	}
	f.server.FlushAll()
	_, err = f.service.Authenticate(f.ctx, rotated.AccessToken)
	require.Equal(t, 20103, errs.From(err).Code)
	_, err = f.service.Authenticate(f.ctx, peer.AccessToken)
	require.NoError(t, err)
	_, err = f.service.Refresh(f.ctx, peer.RefreshToken, identity.RequestMeta{})
	require.NoError(t, err)
	currentFamily, err := f.store.Family(f.ctx, 1, f.user.ID, claims.SessionID)
	require.NoError(t, err)
	require.Len(t, currentFamily.Tokens, 1)
	require.Nil(t, currentFamily.Tokens[0].RevokedAt)
	again := f.request(t, "POST", "/auth/logout-others", f.token, nil)
	require.Equal(t, 200, again.Code, again.Body.String())
	validateOpenAPIResponse(t, readIdentityOpenAPI(t), "/api/v1/auth/logout-others", "post", again.Code, again.Body.Bytes())
	notificationDataF3(t, again, &result)
	require.Equal(t, map[string]int{"revoked": 0}, result)
	logoutOthersAudit(t, f, secrets, []int{2}) // BE-IDN-009: unchanged retries add no audit.
	t.Log("BE-IDN-005 #1/2/3/6/7: current bearer retained; two devices and rotated refresh history rejected; revoked=2 then 0; same-tenant peer unaffected; count-only audits persisted")
}

func logoutOthersPeer(t *testing.T, f *accountFixture) identity.Tokens {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	peer := model.User{TenantID: 1, Username: "logout-peer", PasswordHash: f.user.PasswordHash, DepartmentID: f.user.DepartmentID, Status: "active"}
	require.NoError(t, q.Create(&peer).Error)
	tokens, err := f.service.Login(f.ctx, 1, peer.Username, f.password, identity.RequestMeta{})
	require.NoError(t, err)
	return tokens
}

func logoutOthersAudit(t *testing.T, f *accountFixture, tokens []identity.Tokens, counts []int) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct {
		TenantID     int64
		ActorID      int64
		ResourceType string
		ResourceID   string
		Result       string
		After        json.RawMessage
		Record       string
	}
	require.NoError(t, q.Raw(`SELECT tenant_id,actor_id,resource_type,resource_id,result,"after",
 to_jsonb(audit_log)::text AS record FROM audit_log WHERE action=? ORDER BY id`, "system.user.logout.others").Scan(&rows).Error)
	require.Len(t, rows, len(counts))
	ids, err := f.accounts.Store.SessionIDs(f.ctx, 1, f.user.ID)
	require.NoError(t, err)
	for i, row := range rows {
		require.EqualValues(t, 1, row.TenantID)
		require.Equal(t, f.user.ID, row.ActorID)
		require.Equal(t, "user", row.ResourceType)
		require.Equal(t, strconv.FormatInt(f.user.ID, 10), row.ResourceID)
		require.Equal(t, "success", row.Result)
		var snapshot map[string]int
		require.NoError(t, json.Unmarshal(row.After, &snapshot))
		require.Equal(t, map[string]int{"revoked": counts[i]}, snapshot)
		for _, sid := range ids {
			require.False(t, strings.Contains(row.Record, sid), "audit must not contain a session ID")
		}
		for _, pair := range tokens {
			require.False(t, strings.Contains(row.Record, pair.AccessToken), "audit must not contain access credentials")
			require.False(t, strings.Contains(row.Record, pair.RefreshToken), "audit must not contain refresh credentials")
		}
		require.False(t, strings.Contains(row.Record, f.token), "audit must not contain current credentials")
		require.False(t, strings.Contains(row.Record, f.password), "audit must not contain passwords")
	}
}

func TestIdentityLogoutOthersIntegrationSingleAndAllLogoutUnchanged(t *testing.T) {
	// BE-IDN-005 §3 #4/5: single deletion includes self; logout-all still includes caller.
	f := newAccountFixture(t)
	other := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, other.AccessToken)
	require.NoError(t, err)
	deleted := f.request(t, "DELETE", "/auth/sessions/"+claims.SessionID, f.token, nil)
	require.Equal(t, 204, deleted.Code, deleted.Body.String())
	require.Equal(t, 401, f.request(t, "GET", "/auth/sessions", other.AccessToken, nil).Code)
	require.Equal(t, 200, f.request(t, "GET", "/auth/sessions", f.token, nil).Code)
	claims, err = f.service.Authenticate(f.ctx, f.token)
	require.NoError(t, err)
	require.Equal(t, 204, f.request(t, "DELETE", "/auth/sessions/"+claims.SessionID, f.token, nil).Code)
	require.Equal(t, 401, f.request(t, "GET", "/auth/sessions", f.token, nil).Code)
	current, other := f.login(t), f.login(t)
	all := f.request(t, "POST", "/auth/logout-all", current.AccessToken, nil)
	require.Equal(t, 204, all.Code, all.Body.String())
	f.server.FlushAll()
	for _, pair := range []identity.Tokens{current, other} {
		require.Equal(t, 401, f.request(t, "GET", "/auth/sessions", pair.AccessToken, nil).Code)
		_, err = f.service.Refresh(f.ctx, pair.RefreshToken, identity.RequestMeta{})
		require.Equal(t, 20103, errs.From(err).Code)
	}
	t.Log("BE-IDN-005 #4/5: single deletion revokes only its target and supports self-deletion; logout-all returns 204 and revokes caller plus other sessions, surviving Redis loss")
}

func TestIdentityLogoutOthersIntegrationExpiredHistoryAndPasswordRestriction(t *testing.T) {
	// BE-IDN-005 §2: expired families are persisted as revoked; login alone permits logout.
	f := newAccountFixture(t)
	other := f.login(t)
	claims, err := f.service.Authenticate(f.ctx, other.AccessToken)
	require.NoError(t, err)
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_refresh_token SET expires_at=? WHERE tenant_id=1 AND user_id=? AND session_id=?", f.now.Add(-time.Hour), f.user.ID, claims.SessionID))
	require.NoError(t, f.database.Exec(f.ctx, "UPDATE sys_user SET force_password_change=true WHERE id=?", f.user.ID))
	response := f.request(t, "POST", "/auth/logout-others", f.token, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	var result map[string]int
	notificationDataF3(t, response, &result)
	require.Equal(t, map[string]int{"revoked": 1}, result)
	family, err := f.store.Family(f.ctx, 1, f.user.ID, claims.SessionID)
	require.NoError(t, err)
	require.NotEmpty(t, family.Tokens)
	for _, row := range family.Tokens {
		require.NotNil(t, row.RevokedAt)
		require.NotNil(t, row.RevokedReason)
		require.Equal(t, "logout", *row.RevokedReason)
	}
	_, err = f.service.Authenticate(f.ctx, f.token)
	require.NoError(t, err)
	t.Log("BE-IDN-005 expired family counted once and persisted as logout; password-change restriction still permits logout-others; current bearer retained")
}

func TestIdentityLogoutOthersOpenAPIContract(t *testing.T) {
	// BE-IDN-005 §3 #8: bearer-only operation and a required nonnegative count.
	doc := readIdentityOpenAPI(t)
	paths := doc["paths"].(contractMap)
	require.Contains(t, paths, "/api/v1/auth/logout-others")
	operation := paths["/api/v1/auth/logout-others"].(contractMap)["post"].(contractMap)
	require.Equal(t, "logoutOtherSessions", operation["operationId"])
	require.Equal(t, []any{contractMap{"bearerAuth": []any{}}}, operation["security"])
	require.NotContains(t, operation, "x-permission")
	require.NotContains(t, operation, "requestBody")
	responses := operation["responses"].(contractMap)
	require.Contains(t, responses, "200")
	require.Contains(t, responses, "401")
	require.Contains(t, responses, "503")
	response := responses["200"].(contractMap)["content"].(contractMap)["application/json"].(contractMap)
	parts := response["schema"].(contractMap)["allOf"].([]any)
	require.Equal(t, "#/components/schemas/SuccessEnvelope", parts[0].(contractMap)["$ref"])
	data := parts[1].(contractMap)["properties"].(contractMap)["data"].(contractMap)
	result := resolveContract(t, doc, data)
	require.Equal(t, []any{"revoked"}, result["required"])
	require.Equal(t, false, result["additionalProperties"])
	count := result["properties"].(contractMap)["revoked"].(contractMap)
	require.Equal(t, "integer", count["type"])
	require.EqualValues(t, 0, count["minimum"])
	t.Log("BE-IDN-005 #8: OpenAPI declares POST logout-others, bearer authentication, success envelope with required integer revoked>=0, and 401/503 errors")
}
