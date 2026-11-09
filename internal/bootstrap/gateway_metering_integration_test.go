package bootstrap

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func gatewayCalls(t *testing.T, f *gatewayHTTPFixture) *gateway.CallService {
	t.Helper()
	svc, err := gateway.NewCallService(f.gate, func() time.Time { return f.now }, gateway.CallPolicy{
		Timeout: 5 * time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32),
	})
	require.NoError(t, err)
	return svc
}

func gatewayIntent(f *gatewayHTTPFixture, svc *gateway.CallService) gateway.Call {
	return svc.NewCall(gateway.Principal{TenantID: 1, ClientID: f.client.ID}, f.route, "server-trace", "same-client-request", "192.0.2.1")
}

func TestGatewayMeteringIntegrationSingleTerminalAndUsage(t *testing.T) {
	// REQ-GW-007: retries and duplicate terminal delivery share one durable identity.
	f := newGatewayHTTPFixture(t)
	svc := gatewayCalls(t, f)
	call := gatewayIntent(f, svc)
	require.NoError(t, svc.Start(f.ctx, call))
	result := gateway.CallResult{StatusCode: 200, Attempts: 2, Data: json.RawMessage(`{"status":"completed"}`)}
	require.NoError(t, svc.Finish(f.ctx, call.ID, result))
	require.NoError(t, svc.Finish(f.ctx, call.ID, result))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"api_call", "api_call_log", "api_call_meter"} {
		var count int64
		require.NoError(t, q.Table(table).Where("call_id=?", call.ID).Count(&count).Error)
		require.EqualValues(t, 1, count, table)
	}
	var log struct {
		State, IPHash, RequestID string
		MeteredUnits, Attempts   int
		Billed                   bool
		CostUnits, CostCents     int64
	}
	require.NoError(t, q.Table("api_call_log").Select("state,ip_hash,request_id,metered_units,attempts,billed,cost_units,cost_cents").Where("call_id=?", call.ID).Take(&log).Error)
	require.Equal(t, "succeeded", log.State)
	require.Equal(t, 1, log.MeteredUnits)
	require.Equal(t, 2, log.Attempts)
	require.Len(t, log.IPHash, 64)
	require.False(t, log.Billed)
	require.Zero(t, log.CostUnits)
	require.Zero(t, log.CostCents)
	usage, err := svc.Usage(f.ctx, gateway.Principal{TenantID: 1, ClientID: f.client.ID}, f.now.Format("2006-01-02"))
	require.NoError(t, err)
	require.EqualValues(t, 1, usage.Calls)
	require.EqualValues(t, 1, usage.MeteredCalls)
}

func TestGatewayMeteringIntegrationRecoveryDoesNotBill(t *testing.T) {
	// REQ-GW-007: a crash leaves durable processing; reconciliation never invents success.
	f := newGatewayHTTPFixture(t)
	svc := gatewayCalls(t, f)
	call := gatewayIntent(f, svc)
	require.NoError(t, svc.Start(f.ctx, call))
	f.now = call.DeadlineAt.Add(time.Second)
	count, err := svc.Reconcile(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = svc.Reconcile(f.ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var state string
	require.NoError(t, q.Table("api_call_log").Select("state").Where("call_id=?", call.ID).Scan(&state).Error)
	require.Equal(t, "failed", state)
	var units int64
	require.NoError(t, q.Table("api_call_meter").Where("call_id=?", call.ID).Count(&units).Error)
	require.Zero(t, units)
	require.Error(t, svc.Finish(f.ctx, call.ID, gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{}`)}))
}
