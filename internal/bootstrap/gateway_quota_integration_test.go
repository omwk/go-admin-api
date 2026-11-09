package bootstrap

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func gatewaySetQuota(t *testing.T, f *gatewayHTTPFixture, daily, monthly int64) {
	t.Helper()
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), f.client.ID, f.route.ID, "active", nil, gateway.QuotaLimits{Daily: daily, Monthly: monthly}))
}

func TestGatewayQuotaIntegrationUTCDayMonthAndRestart(t *testing.T) {
	// REQ-GW-004: the persistence keys follow UTC, including across process restarts.
	f := newGatewayHTTPFixture(t)
	utc := time.Now().UTC()
	f.now = time.Date(utc.Year(), utc.Month(), 15, 23, 59, 59, 0, time.UTC).In(time.FixedZone("local", 8*3600))
	gatewaySetQuota(t, f, 1, 2)
	svc := gatewayCalls(t, f)
	first := gatewayIntent(f, svc)
	require.NoError(t, svc.Start(f.ctx, first))
	require.NoError(t, svc.Finish(f.ctx, first.ID, gateway.CallResult{StatusCode: 502, ErrorCode: "80301", Attempts: 2}))
	var limited *gateway.LimitError
	require.ErrorAs(t, svc.Start(f.ctx, gatewayIntent(f, svc)), &limited)
	require.Equal(t, "daily_quota", limited.Decision.LimitType)
	require.Equal(t, time.Second, limited.Decision.RetryAfter)
	f.now = f.now.Add(2 * time.Second)
	require.NoError(t, svc.Start(f.ctx, gatewayIntent(f, svc)))
	svc = gatewayCalls(t, f)
	f.server.FlushAll()
	require.ErrorAs(t, svc.Start(f.ctx, gatewayIntent(f, svc)), &limited)
	require.Equal(t, "monthly_quota", limited.Decision.LimitType)
	require.Greater(t, limited.Decision.RetryAfter, 24*time.Hour)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var rows []gatewayQuotaRow
	require.NoError(t, q.Table("api_quota").Select("tenant_id,client_id,quota_key,admitted_count,reset_at").Order("quota_key").Find(&rows).Error)
	require.Len(t, rows, 3)
	var total int64
	for _, row := range rows {
		total += row.AdmittedCount
	}
	require.EqualValues(t, 4, total, "two admissions each reserve one daily and one monthly count")
	month := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	f.now = month.AddDate(0, 1, 0)
	require.NoError(t, svc.Start(f.ctx, gatewayIntent(f, svc)))
	var meters int64
	require.NoError(t, q.Table("api_call_meter").Count(&meters).Error)
	require.Zero(t, meters)
}

func TestGatewayQuotaIntegrationConcurrentAtomicAdmission(t *testing.T) {
	// REQ-GW-004/005: two instances cannot oversubscribe a persisted N/N+1 boundary.
	f := newGatewayHTTPFixture(t)
	gatewaySetQuota(t, f, 7, 10)
	services := []*gateway.CallService{gatewayCalls(t, f), gatewayCalls(t, f)}
	var workers sync.WaitGroup
	results := make(chan error, 24)
	for i := 0; i < cap(results); i++ {
		svc := services[i%2]
		call := gatewayIntent(f, svc)
		workers.Add(1)
		go func() { defer workers.Done(); results <- svc.Start(f.ctx, call) }()
	}
	workers.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var limited *gateway.LimitError
		require.True(t, errors.As(err, &limited), "unexpected admission error: %v", err)
		rejected++
	}
	require.Equal(t, 7, accepted)
	require.Equal(t, 17, rejected)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var counts []int64
	require.NoError(t, q.Table("api_quota").Order("quota_key").Pluck("admitted_count", &counts).Error)
	require.Equal(t, []int64{7, 7}, counts)
	var calls int64
	require.NoError(t, q.Table("api_call").Count(&calls).Error)
	require.EqualValues(t, 7, calls)
	require.NoError(t, q.Table("api_call_log").Count(&calls).Error)
	require.EqualValues(t, 7, calls)
}

func TestGatewayQuotaIntegrationPolicyReductionAndApplicationIsolation(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	svc := gatewayCalls(t, f)
	for i := 0; i < 2; i++ {
		require.NoError(t, svc.Start(f.ctx, gatewayIntent(f, svc)))
	}
	gatewaySetQuota(t, f, 1, 1)
	var limited *gateway.LimitError
	require.ErrorAs(t, svc.Start(f.ctx, gatewayIntent(f, svc)), &limited)
	other, _, err := f.life.Create(f.ctx, f.gatewayActor(), "isolated-application", f.gatewayActor().UserID, nil)
	require.NoError(t, err)
	require.NoError(t, f.gate.GrantRoute(f.ctx, f.gatewayActor(), other.ID, f.route.ID, "active", nil, gateway.QuotaLimits{Daily: 1, Monthly: 1}))
	call := svc.NewCall(gateway.Principal{TenantID: 1, ClientID: other.ID}, f.route, "trace", "same-observation", "192.0.2.1")
	require.NoError(t, svc.Start(f.ctx, call))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var counts []int64
	require.NoError(t, q.Table("api_quota").Where("client_id=?", f.client.ID).Order("quota_key").Pluck("admitted_count", &counts).Error)
	require.Equal(t, []int64{2, 2}, counts, "lowering policy never resets persisted admission")
}
