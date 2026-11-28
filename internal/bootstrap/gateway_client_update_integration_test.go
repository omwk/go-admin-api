package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestGatewayClientUpdateIntegrationMetadataAndCIDR(t *testing.T) {
	// BE-IDN-034 §3: metadata updates preserve app identity and enforce normalized CIDR rules.
	f := newGatewayHTTPFixture(t)
	doc := observationContract(t)
	path := fmt.Sprintf("/api-clients/%d", f.client.ID)
	contractPath := "/api/v1/admin/api-clients/{id}"
	read := func() gateway.ClientInfo {
		w, body := f.manage(t, http.MethodGet, path, nil, "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var client gateway.ClientInfo
		require.NoError(t, json.Unmarshal(body["data"], &client))
		return client
	}
	before := read()
	var fingerprints []string
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("api_client_secret").Where("tenant_id=? AND client_id=?", 1, f.client.ID).Order("id").Pluck("fingerprint", &fingerprints).Error)

	name := "renamed client"
	w, body := f.manage(t, http.MethodPut, path, map[string]any{"name": name, "version": before.Version}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	validateOpenAPIResponse(t, doc, contractPath, "put", w.Code, w.Body.Bytes())
	var updated gateway.ClientInfo
	require.NoError(t, json.Unmarshal(body["data"], &updated))
	require.Equal(t, before.AppID, updated.AppID)
	require.Equal(t, name, updated.Name)
	got := read()
	require.Equal(t, name, got.Name)
	require.Equal(t, before.AppID, got.AppID)
	require.Equal(t, before.Status, got.Status)
	require.Equal(t, before.IPWhitelist, got.IPWhitelist)
	t.Logf("#1 GET before app_id=%s name=%s; GET after app_id=%s name=%s", before.AppID, before.Name, got.AppID, got.Name)
	var afterFingerprints []string
	require.NoError(t, q.Table("api_client_secret").Where("tenant_id=? AND client_id=?", 1, f.client.ID).Order("id").Pluck("fingerprint", &afterFingerprints).Error)
	require.Equal(t, fingerprints, afterFingerprints)

	w, body = f.manage(t, http.MethodPut, path, map[string]any{"ip_whitelist": []string{"10.1.2.3/8"}, "version": got.Version}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(body["data"], &updated))
	require.Equal(t, []string{"10.0.0.0/8"}, updated.IPWhitelist)
	require.Equal(t, name, updated.Name)
	require.Equal(t, before.Status, updated.Status)

	request := f.sign(t, f.publicPath(), f.key.Secret)
	// The request carries the simulated network peer through the real gateway handler.
	request.RemoteAddr = "10.1.2.3:45000"
	r, _ := f.serve(t, request)
	require.Equal(t, 200, r.Code, r.Body.String())
	request = f.sign(t, f.publicPath(), f.key.Secret)
	request.RemoteAddr = "11.0.0.1:45000"
	r, body = f.serve(t, request)
	require.Equal(t, 403, r.Code, r.Body.String())
	require.JSONEq(t, `80105`, string(body["code"]))
	t.Logf("#8.2 real gateway HTTP: 10.1.2.3 status=%d; 11.0.0.1 status=%d code=%s", 200, r.Code, body["code"])

	w, body = f.manage(t, http.MethodPut, path, map[string]any{"ip_whitelist": []string{}, "version": updated.Version}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(body["data"], &updated))
	require.Empty(t, updated.IPWhitelist)
	w, body = f.manage(t, http.MethodPut, path, map[string]any{"status": "suspended", "version": updated.Version}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(body["data"], &updated))
	require.Equal(t, "suspended", updated.Status)
	w, body = f.manage(t, http.MethodPut, path, map[string]any{"name": "stale", "version": updated.Version - 1}, "")
	require.Equal(t, 409, w.Code, w.Body.String())
	require.Equal(t, "10409", string(body["code"]))
	t.Logf("#7 stale version response status=409 code=%s", body["code"])

	for _, input := range []map[string]any{
		{"version": updated.Version},
		{"name": " ", "version": updated.Version},
		{"name": "valid", "status": "", "version": updated.Version},
		{"ip_whitelist": []string{"not-an-ip"}, "version": updated.Version},
		{"ip_whitelist": []string{"10.0.0.0/33"}, "version": updated.Version},
		{"ip_whitelist": []string{"0.0.0.0/0"}, "version": updated.Version},
		{"ip_whitelist": repeatedWhitelist(101), "version": updated.Version},
	} {
		w, _ = f.manage(t, http.MethodPut, path, input, "")
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	w, body = f.manage(t, http.MethodPost, "/api-clients", map[string]any{"name": "cidr-created", "ip_whitelist": []string{"10.0.0.0/8"}}, "")
	require.Equal(t, 201, w.Code, w.Body.String())
	var created struct {
		Client gateway.ClientInfo `json:"client"`
	}
	require.NoError(t, json.Unmarshal(body["data"], &created))
	require.Equal(t, []string{"10.0.0.0/8"}, created.Client.IPWhitelist)
	w, _ = f.manage(t, http.MethodPost, "/api-clients", map[string]any{"name": "invalid-cidr", "ip_whitelist": []string{"10.0.0.0/33"}}, "")
	require.Equal(t, 400, w.Code, w.Body.String())
	t.Log("#8.1 POST CIDR whitelist status=201; invalid CIDR status=400")

	actor := f.gatewayActor()
	_, err := f.gate.UpdateClient(f.ctx, gateway.Actor{TenantID: actor.TenantID + 1, UserID: actor.UserID}, f.client.ID, gateway.ClientUpdate{Name: &name}, updated.Version)
	require.Equal(t, 404, gatewayErrorStatus(err))
	_, err = f.gate.UpdateClient(f.ctx, gateway.Actor{TenantID: actor.TenantID, UserID: actor.UserID + 1}, f.client.ID, gateway.ClientUpdate{Name: &name}, updated.Version)
	require.Equal(t, 404, gatewayErrorStatus(err))
	t.Log("#10 cross-tenant and non-owner update results are both 404")

	var auditRows []struct {
		Before string `gorm:"column:before_json"`
		After  string `gorm:"column:after_json"`
	}
	require.NoError(t, q.Raw(`SELECT "before"::text AS before_json,"after"::text AS after_json FROM audit_log WHERE action='gateway.client.update' AND resource_id=? ORDER BY id`, fmt.Sprint(f.client.ID)).Scan(&auditRows).Error)
	require.Len(t, auditRows, 4)
	require.JSONEq(t, `{"name":"status-reader"}`, auditRows[0].Before)
	require.JSONEq(t, `{"name":"renamed client"}`, auditRows[0].After)
	require.JSONEq(t, `{"ip_whitelist":[]}`, auditRows[1].Before)
	require.JSONEq(t, `{"ip_whitelist":["10.0.0.0/8"]}`, auditRows[1].After)
	require.JSONEq(t, `{"ip_whitelist":["10.0.0.0/8"]}`, auditRows[2].Before)
	require.JSONEq(t, `{"ip_whitelist":[]}`, auditRows[2].After)
	require.JSONEq(t, `{"status":"active"}`, auditRows[3].Before)
	require.JSONEq(t, `{"status":"suspended"}`, auditRows[3].After)
	t.Logf("#9 gateway.client.update records only changed fields across %d updates", len(auditRows))
}

func gatewayErrorStatus(err error) int {
	if err == nil {
		return 200
	}
	return errs.From(err).HTTPStatus
}

func repeatedWhitelist(length int) []string {
	values := make([]string, length)
	for i := range values {
		values[i] = "10.0.0.1"
	}
	return values
}
