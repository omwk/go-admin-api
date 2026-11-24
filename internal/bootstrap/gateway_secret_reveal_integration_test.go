package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

const (
	listKeysContractPath = "/api/v1/admin/api-clients/{id}/secrets"
	revealContractPath   = listKeysContractPath + "/{sid}/reveal"
)

func TestGatewaySecretRevealIntegrationHTTP(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	doc := observationContract(t)
	f.markers = append(f.markers, f.key.Secret)
	for range 2 {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin"+secretRevealPath(f.client.ID, f.key.ID), nil)
		r.Header.Set("Authorization", "Bearer "+f.bearer)
		r.Header.Set("User-Agent", f.key.Secret)
		w, body := f.serve(t, r)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var out gateway.RevealedSecret
		require.NoError(t, json.Unmarshal(body["data"], &out))
		require.True(t, out.Secret == f.key.Secret, "the revealed value must match issuance")
		require.Equal(t, f.key.SecretPrefix, out.Prefix)
		require.Nil(t, out.ExpiresAt)
		var dataFields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body["data"], &dataFields))
		require.ElementsMatch(t, []string{"secret", "prefix", "expires_at"}, mapKeys(dataFields))
		dataFields["secret"] = json.RawMessage(`"redacted"`)
		var err error
		body["data"], err = json.Marshal(dataFields)
		require.NoError(t, err)
		redacted, err := json.Marshal(body)
		require.NoError(t, err)
		validateOpenAPIResponse(t, doc, revealContractPath, "get", w.Code, redacted)
	}
	t.Log("#1 both reveals match the originally issued value and prefix, expires_at=null")
	t.Log("#6 Cache-Control: no-store")
	assertSecretRevealAudit(t, f)
	f.noLeaks(t, "HTTP logs", f.logs.text())

	listPath := resourcePath("api-clients", f.client.ID) + "/secrets"
	assertSecretRevealable(t, f, doc, listPath, true)
	w, _ := f.manage(t, http.MethodDelete, listPath+fmt.Sprintf("/%d", f.key.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	assertSecretRevealable(t, f, doc, listPath, false)
	w, body := f.manage(t, http.MethodGet, secretRevealPath(f.client.ID, f.key.ID), nil, "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, "10003", string(body["code"]))
	validateOpenAPIResponse(t, doc, revealContractPath, "get", w.Code, w.Body.Bytes())
	t.Log("#4 revoked=404")
	t.Log("#7 revealable active=true, revoked=false")
}

func assertSecretRevealAudit(t *testing.T, f *gatewayHTTPFixture) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []struct {
		Stored, Evidence string
	}
	require.NoError(t, q.Raw(`SELECT to_jsonb(a)::text AS stored,
		jsonb_build_object('action',action,'client_id',resource_id,'actor_id',actor_id,'ip',host(ip),
		'before',"before",'after',"after",'result',result)::text AS evidence
		FROM audit_log a WHERE action='gateway.secret.reveal' ORDER BY id`).Scan(&rows).Error)
	require.Equal(t, 2, len(rows))
	for _, row := range rows {
		f.noLeaks(t, "complete audit row", row.Stored)
		require.JSONEq(t, fmt.Sprintf(`{"action":"gateway.secret.reveal","client_id":"%d","actor_id":%d,"ip":"192.0.2.1","before":null,"after":{"secret_id":%d},"result":"success"}`,
			f.client.ID, f.gatewayActor().UserID, f.key.ID), row.Evidence)
	}
	t.Logf("#2 audit rows=%d, first row=%s", len(rows), rows[0].Evidence)
}

func assertSecretRevealable(t *testing.T, f *gatewayHTTPFixture, doc contractMap, path string, want bool) {
	t.Helper()
	w, body := f.manage(t, http.MethodGet, path, nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	f.noLeaks(t, "secret list", w.Body.String())
	validateOpenAPIResponse(t, doc, listKeysContractPath, "get", w.Code, w.Body.Bytes())
	var out []struct {
		ID         int64 `json:"id"`
		Revealable bool  `json:"revealable"`
	}
	require.NoError(t, json.Unmarshal(body["data"], &out))
	require.Len(t, out, 1)
	require.Equal(t, f.key.ID, out[0].ID)
	require.Equal(t, want, out[0].Revealable)
}

func TestGatewaySecretRevealIntegrationPermissionsAndScope(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	doc := observationContract(t)
	foreign, key := foreignRevealKey(t, f)
	f.markers = append(f.markers, key.Secret, f.key.Secret)
	for _, path := range []string{secretRevealPath(foreign.ID, key.ID), secretRevealPath(f.client.ID, key.ID)} {
		w, body := f.manage(t, http.MethodGet, path, nil, "")
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Equal(t, "10003", string(body["code"]))
		f.noLeaks(t, "cross-tenant response", w.Body.String())
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	expiry := f.now.Add(time.Minute)
	require.NoError(t, q.Table("api_client_secret").Where("id=?", f.key.ID).Update("expires_at", expiry).Error)
	w, body := f.manage(t, http.MethodGet, secretRevealPath(f.client.ID, f.key.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	var out gateway.RevealedSecret
	require.NoError(t, json.Unmarshal(body["data"], &out))
	require.NotNil(t, out.ExpiresAt)
	require.Equal(t, expiry, *out.ExpiresAt)
	require.NoError(t, q.Table("api_client_secret").Where("id=?", f.key.ID).Update("expires_at", f.now).Error)
	w, body = f.manage(t, http.MethodGet, secretRevealPath(f.client.ID, f.key.ID), nil, "")
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, "10003", string(body["code"]))
	assertSecretRevealable(t, f, doc, resourcePath("api-clients", f.client.ID)+"/secrets", false)
	t.Log("#4 cross-tenant, mismatched client, and expires_at=now all returned HTTP 404")
	t.Log("#7 revealable expired=false")

	ordinaryScopeActor(t, f.identityFixture, f.gatewayActor().UserID)
	require.NoError(t, q.Exec(`DELETE FROM sys_role_permission rp
		USING sys_user_role ur, sys_permission p
		WHERE ur.tenant_id=rp.tenant_id AND ur.role_id=rp.role_id AND p.tenant_id=rp.tenant_id
		AND p.id=rp.permission_id AND ur.tenant_id=? AND ur.user_id=? AND p.code<>?`,
		f.gatewayActor().TenantID, f.gatewayActor().UserID, permission.GatewayClientList).Error)
	require.NoError(t, q.Table("api_client_secret").Where("id=?", f.key.ID).Update("expires_at", nil).Error)
	w, _ = f.manage(t, http.MethodGet, resourcePath("api-clients", f.client.ID)+"/secrets", nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	w, body = f.manage(t, http.MethodGet, secretRevealPath(f.client.ID, f.key.ID), nil, "")
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, "20107", string(body["code"]))
	f.noLeaks(t, "list-only response", w.Body.String())
	validateOpenAPIResponse(t, doc, revealContractPath, "get", w.Code, w.Body.Bytes())
	t.Log("#3 gateway:client:list alone gives list=200, reveal=403 code=20107")
}

func TestGatewaySecretRevealIntegrationDailyLimit(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	f.markers = append(f.markers, f.key.Secret)
	seedPreviousDayReveals(t, f)
	path := secretRevealPath(f.client.ID, f.key.ID)
	for range 20 {
		w, _ := f.manage(t, http.MethodGet, path, nil, "")
		require.Equal(t, http.StatusOK, w.Code)
	}
	w, body := f.manage(t, http.MethodGet, path, nil, "")
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "10005", string(body["code"]))
	f.noLeaks(t, "quota denial", w.Body.String())
	validateOpenAPIResponse(t, observationContract(t), revealContractPath, "get", w.Code, w.Body.Bytes())
	t.Logf("#5 request 21: HTTP %d %s", w.Code, w.Body.String())

	foreign, key := foreignRevealKey(t, f)
	tokens, err := f.identityFixture.service.Login(f.ctx, foreign.TenantID, "foreign-operator", f.password, identity.RequestMeta{})
	require.NoError(t, err)
	f.bearer = tokens.AccessToken
	w, _ = f.manage(t, http.MethodGet, secretRevealPath(foreign.ID, key.ID), nil, "")
	require.Equal(t, http.StatusOK, w.Code)
	t.Log("#5 previous UTC day rows excluded, another tenant still returns HTTP 200")
}

func seedPreviousDayReveals(t *testing.T, f *gatewayHTTPFixture) {
	t.Helper()
	require.NoError(t, f.database.Exec(f.ctx, `INSERT INTO audit_log
		(tenant_id, actor_type, actor_id, action, resource_type, resource_id, "after", trace_id, result, created_at)
		SELECT ?, 'user', ?, 'gateway.secret.reveal', 'api_client', ?, jsonb_build_object('secret_id', ?::bigint),
			'previous-day-reveal', 'success', (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') - interval '1 second'
		FROM generate_series(1, 20)`, f.gatewayActor().TenantID, f.gatewayActor().UserID, fmt.Sprint(f.client.ID), f.key.ID))
}

func TestGatewaySecretRevealIntegrationConcurrentQuota(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	actor := gateway.Actor{TenantID: f.gatewayActor().TenantID, UserID: f.user.ID}
	client, key, err := f.life.Create(f.ctx, actor, "second-client", actor.UserID, nil)
	require.NoError(t, err)
	path := secretRevealPath(f.client.ID, f.key.ID)
	for range 19 {
		w, _ := f.manage(t, http.MethodGet, path, nil, "")
		require.Equal(t, http.StatusOK, w.Code)
	}
	start := make(chan struct{})
	statuses := make(chan int, 2)
	for _, candidate := range []string{path, secretRevealPath(client.ID, key.ID)} {
		go func() {
			<-start
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin"+candidate, nil)
			r.Header.Set("Authorization", "Bearer "+f.bearer)
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			statuses <- w.Code
		}()
	}
	close(start)
	require.ElementsMatch(t, []int{http.StatusOK, http.StatusTooManyRequests}, []int{<-statuses, <-statuses})
	t.Log("#5 two clients racing for the last tenant slot yield one HTTP 200 and one HTTP 429")
}

func TestGatewaySecretRevealIntegrationAuditFailure(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	f.markers = append(f.markers, f.key.Secret)
	require.NoError(t, f.database.Exec(f.ctx, `CREATE FUNCTION reject_gateway_reveal_audit() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reveal audit unavailable'; END; $$`))
	require.NoError(t, f.database.Exec(f.ctx, `CREATE TRIGGER reject_gateway_reveal_audit BEFORE INSERT ON audit_log
		FOR EACH ROW WHEN (NEW.action = 'gateway.secret.reveal') EXECUTE FUNCTION reject_gateway_reveal_audit()`))
	path := secretRevealPath(f.client.ID, f.key.ID)
	w, body := f.manage(t, http.MethodGet, path, nil, "")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, body, "data")
	f.noLeaks(t, "audit failure response", w.Body.String())
	f.noLeaks(t, "audit failure logs", f.logs.text())
	t.Log("Audit insert failure returns HTTP 503 without key material")
}

func foreignRevealKey(t *testing.T, f *gatewayHTTPFixture) (gateway.Client, gateway.IssuedSecret) {
	t.Helper()
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	tenant := model.Tenant{Code: "reveal-foreign", Name: "Foreign", Status: "active", Settings: json.RawMessage(`{}`)}
	require.NoError(t, q.Create(&tenant).Error)
	dept := model.Department{TenantID: tenant.ID, Name: "Foreign", Path: "1", Status: "active"}
	require.NoError(t, q.Create(&dept).Error)
	user := model.User{TenantID: tenant.ID, Username: "foreign-operator", DepartmentID: dept.ID,
		PasswordHash: f.user.PasswordHash, IsSuper: true, Status: "active"}
	require.NoError(t, q.Create(&user).Error)
	actor := gateway.Actor{TenantID: tenant.ID, UserID: user.ID}
	client, key, err := f.life.Create(f.ctx, actor, "foreign-client", actor.UserID, nil)
	require.NoError(t, err)
	return client, key
}

func secretRevealPath(client, secret int64) string {
	return fmt.Sprintf("/api-clients/%d/secrets/%d/reveal", client, secret)
}
