package bootstrap

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func observationLog(t *testing.T, f *gatewayHTTPFixture, tenant, client, route int64, at time.Time, status, latency int, trace string) {
	t.Helper()
	row := map[string]any{"call_id": ulid.Make().String(), "tenant_id": tenant, "client_id": client, "route_id": route,
		"trace_id": trace, "method": "GET", "path": f.route.Path, "called_at": at,
		"status_code": status, "latency_ms": latency, "req_bytes": 0, "resp_bytes": 0, "state": "processing"}
	if status != 0 {
		row["state"], row["finished_at"], row["cost_cents"] = "failed", at.Add(time.Duration(latency)*time.Millisecond), int64(7)
		if status >= 200 && status < 300 {
			row["state"] = "succeeded"
		}
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("api_call_log").Create(row).Error)
}

func observationPage(t *testing.T, f *gatewayHTTPFixture, query string) gateway.CallPage {
	t.Helper()
	w, body := f.manage(t, "GET", "/api-calls"+query, nil, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var page gateway.CallPage
	require.NoError(t, json.Unmarshal(body["data"], &page))
	for _, forbidden := range []string{"request_body", "response_body", "result", "ip_hash", "secret", "cipher_text"} {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body["data"], &payload))
		for _, item := range payload["list"].([]any) {
			require.NotContains(t, item.(map[string]any), forbidden)
		}
	}
	return page
}

func observationTrial(t *testing.T, f *gatewayHTTPFixture, in gateway.TryInput) gateway.TryResult {
	t.Helper()
	w, body := f.manage(t, "POST", fmt.Sprintf("/api-clients/%d/try", f.client.ID), in, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var result gateway.TryResult
	require.NoError(t, json.Unmarshal(body["data"], &result))
	require.True(t, result.Test)
	require.False(t, result.Billing.Billed)
	return result
}

func observationInput(f *gatewayHTTPFixture) gateway.TryInput {
	return gateway.TryInput{RouteID: f.route.ID, Path: f.publicPath(), Body: json.RawMessage(`null`)}
}

func observationRange(f *gatewayHTTPFixture) string {
	v := url.Values{"from": {f.now.Add(-48 * time.Hour).Format(time.RFC3339)}, "to": {f.now.Add(time.Second).Format(time.RFC3339)}}
	return v.Encode()
}
