package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestGatewayObservabilityIntegrationTrialNoBilling(t *testing.T) {
	// BE-IDN-029 acceptance 6/7: real signature + reader, durable metadata only.
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	require.NoError(t, q.Table("api_route").Where("id=?", f.route.ID).Updates(map[string]any{"billing_mode": "per_call", "price_cents": 7, "units_per_call": 1, "currency": "CNY"}).Error)
	charges, err := newGatewayBilling(f.database, func() time.Time { return f.now })
	require.NoError(t, err)
	calls, err := gateway.NewCallService(f.gate, func() time.Time { return f.now }, gateway.CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)}, charges)
	require.NoError(t, err)
	open, ok := gatewayMeteredHTTP(t, f, nil, nil, calls).(*gateway.OpenHandler)
	require.True(t, ok)
	f.trialOpen = open
	var before int64
	require.NoError(t, q.Table("api_call_meter").Count(&before).Error)
	result := observationTrial(t, f, observationInput(f))
	require.Equal(t, 200, result.StatusCode)
	require.Zero(t, result.ErrorCode)
	require.GreaterOrEqual(t, result.LatencyMS, int64(0))
	require.NotEmpty(t, result.Response)
	var after int64
	require.NoError(t, q.Table("api_call_meter").Count(&after).Error)
	require.Equal(t, before, after)
	for _, table := range []string{"call_charge", "wallet_txn", "unit_txn", "api_quota"} {
		var count int64
		require.NoError(t, q.Table(table).Count(&count).Error)
		require.Zero(t, count, table)
	}
	var log struct {
		ChargeSource                       string
		Billed                             bool
		CostCents, CostUnits, MeteredUnits int64
	}
	require.NoError(t, q.Table("api_call_log").Select("charge_source,billed,cost_cents,cost_units,metered_units").Where("call_id=?", result.CallID).Take(&log).Error)
	require.Equal(t, "manual_test", log.ChargeSource)
	require.False(t, log.Billed)
	require.Zero(t, log.CostCents+log.CostUnits+log.MeteredUnits)
	call, err := f.gate.Call(f.ctx, result.CallID)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(call.Result))
	page := observationPage(t, f, "?to="+f.now.Add(time.Second).Format(time.RFC3339))
	require.EqualValues(t, 1, page.Total)
	require.True(t, page.List[0].Test)
	t.Logf("#6 status=%d latency_ms=%d log=manual_test test=true rows=1; #7 SELECT count(*) FROM api_call_meter before=%d after=%d; call_charge/wallet_txn/unit_txn/api_quota rows=0; stored result={}", result.StatusCode, result.LatencyMS, before, after)
}

func TestGatewayObservabilityIntegrationTrialGates(t *testing.T) {
	// BE-IDN-029 acceptance 8: all gates execute on the real signed request.
	f := newGatewayHTTPFixture(t)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, tc := range []struct {
		name, table, column string
		value, restore      any
		status, code        int
	}{
		{"ungranted", "api_client_route", "status", "suspended", "active", 403, 80104},
		{"disabled route", "api_route", "status", "disabled", "enabled", 404, 80201},
		{"private route", "api_route", "visibility", "private", "public", 404, 80201},
		{"disabled client", "api_client", "status", "suspended", "active", 401, 80101},
		{"revoked key", "api_client_secret", "status", "revoked", "active", 401, 80102},
	} {
		t.Run(tc.name, func(t *testing.T) {
			predicate, id := "id=?", f.route.ID
			if tc.table == "api_client_route" {
				predicate, id = "client_id=?", f.client.ID
			}
			if tc.table == "api_client" {
				id = f.client.ID
			}
			if tc.table == "api_client_secret" {
				id = f.key.ID
			}
			require.NoError(t, q.Table(tc.table).Where(predicate, id).Update(tc.column, tc.value).Error)
			result := observationTrial(t, f, observationInput(f))
			require.Equal(t, tc.status, result.StatusCode)
			require.Equal(t, tc.code, result.ErrorCode)
			require.NoError(t, q.Table(tc.table).Where(predicate, id).Update(tc.column, tc.restore).Error)
		})
	}
	require.NoError(t, q.Exec("UPDATE api_client SET ip_whitelist=ARRAY['203.0.113.9'::inet],version=version+1 WHERE id=?", f.client.ID).Error)
	result := observationTrial(t, f, observationInput(f))
	require.Equal(t, 403, result.StatusCode)
	require.Equal(t, 80105, result.ErrorCode)
	require.NoError(t, q.Exec("UPDATE api_client SET ip_whitelist=ARRAY[]::inet[],version=version+1 WHERE id=?", f.client.ID).Error)
	for _, kind := range []string{"daily", "monthly"} {
		require.NoError(t, q.Table("api_client_route").Where("client_id=?", f.client.ID).Update("quota_"+kind, 1).Error)
		key := f.now.Format("2006-01-02")
		if kind == "monthly" {
			key = f.now.Format("2006-01")
		}
		row := map[string]any{"tenant_id": 1, "client_id": f.client.ID, "quota_key": fmt.Sprintf("quota:%d:%s", f.client.ID, key), "admitted_count": 1, "reset_at": f.now.Add(24 * time.Hour)}
		require.NoError(t, q.Table("api_quota").Create(row).Error)
		result := observationTrial(t, f, observationInput(f))
		require.Equal(t, 429, result.StatusCode)
		require.Equal(t, 10005, result.ErrorCode)
		var count int64
		require.NoError(t, q.Table("api_quota").Select("admitted_count").Where("quota_key=?", row["quota_key"]).Scan(&count).Error)
		require.EqualValues(t, 1, count)
		require.NoError(t, q.Table("api_quota").Where("quota_key=?", row["quota_key"]).Delete(nil).Error)
		require.NoError(t, q.Table("api_client_route").Where("client_id=?", f.client.ID).Update("quota_"+kind, 0).Error)
	}
	t.Log("#8 ungranted=>403/80104; disabled/private=>404; disabled client/revoked key=>401; IP=>403/80105; daily/monthly exhausted=>429/10005 and admitted_count stays 1")
}

func TestGatewayObservabilityIntegrationTrialTruncation(t *testing.T) {
	// BE-IDN-029 acceptance 9: real handler with an oversized reader port response.
	f := newGatewayHTTPFixture(t)
	reader := gatewayStatusFunc(func(context.Context, gateway.Principal, string) (gateway.OpenOrderStatus, error) {
		return gateway.OpenOrderStatus{ID: f.grant.PublicID, OrderNo: strings.Repeat("界", 800000), Status: "completed"}, nil
	})
	open, ok := gatewayMeteredHTTP(t, f, reader, nil).(*gateway.OpenHandler)
	require.True(t, ok)
	f.trialOpen = open
	result := observationTrial(t, f, observationInput(f))
	require.Equal(t, 200, result.StatusCode)
	require.True(t, result.Truncated)
	response, ok := result.Response.(string)
	require.True(t, ok)
	require.LessOrEqual(t, len(response), gateway.MaxTrialResponseBytes)
	require.Greater(t, len(response), gateway.MaxTrialResponseBytes-4)
	call, err := f.gate.Call(f.ctx, result.CallID)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(call.Result))
	t.Logf("#9 original response >2400000 bytes; truncated=true; captured=%d <=262144; result body not stored", len(response))
}
