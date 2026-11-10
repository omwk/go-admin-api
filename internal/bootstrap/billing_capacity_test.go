//go:build gatewaybench

package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/billing"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

type billingCapacityReport struct {
	At           time.Time                 `json:"at"`
	Platform     string                    `json:"platform"`
	Samples      int                       `json:"samples_per_level"`
	DatabasePool int                       `json:"database_pool"`
	RedisPool    int                       `json:"redis_pool"`
	Scenarios    []billingCapacityScenario `json:"scenarios"`
}

type billingCapacityScenario struct {
	Source    string                      `json:"source"`
	Closed    gatewayBenchLevel           `json:"closed"`
	Fixed     billingFixedArrival         `json:"fixed"`
	Phases    map[string]gatewayBenchStat `json:"phases"`
	Calls     int64                       `json:"verified_calls"`
	CashCents int64                       `json:"verified_cash_cents"`
	Units     int64                       `json:"verified_units"`
}

// TestBillingCapacity is explicitly gated; timing never decides ordinary CI.
func TestBillingCapacity(t *testing.T) {
	if os.Getenv("CH_GATEWAY_BENCHMARK") != "1" {
		t.Skip("explicit capacity opt-in required")
	}
	n, err := strconv.Atoi(os.Getenv("CH_GATEWAY_BENCHMARK_SAMPLES"))
	require.NoError(t, err)
	require.True(t, n >= 100 && n <= 100000)
	report := billingCapacityReport{At: time.Now().UTC(), Platform: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), Samples: n, DatabasePool: 10, RedisPool: 32}
	for _, source := range []string{"wallet", "paid"} {
		report.Scenarios = append(report.Scenarios, measureBillingCapacity(t, n, source))
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("CH_GATEWAY_BENCHMARK_OUTPUT"), raw, 0600))
	for _, s := range report.Scenarios {
		require.Zero(t, s.Closed.Errors)
		require.Zero(t, s.Fixed.Errors)
		t.Logf("source=%s closed_p50/p95/p99=%.3f/%.3f/%.3f overhead_p99=%.3f fixed50_p99=%.3f qps=%.2f calls=%d cents=%d units=%d", s.Source, s.Closed.Open.P50, s.Closed.Open.P95, s.Closed.Open.P99, s.Closed.Overhead.P99, s.Fixed.Latency.P99, s.Fixed.ActualQPS, s.Calls, s.CashCents, s.Units)
	}
}

func measureBillingCapacity(t *testing.T, n int, source string) billingCapacityScenario {
	t.Helper()
	b, money, wallet := newBillingCapacity(t, source)
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := b.snapshot(r.Context(), b.p, b.status.ID)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": value}); err != nil {
			return
		}
	}))
	defer direct.Close()
	open := httptest.NewServer(b.open)
	defer open.Close()
	result := billingCapacityScenario{Source: source, Closed: b.measureHTTP(t, direct.URL, open.URL+b.f.publicPath(), n, 1)}
	reader, err := NewGatewayOrderReader(b.f.database)
	require.NoError(t, err)
	charges, err := newGatewayBilling(b.f.database, time.Now)
	require.NoError(t, err)
	realHandler, err := gateway.NewOpenHandler(b.auth, b.f.gate, reader, b.calls, b.limits, time.Now, charges)
	require.NoError(t, err)
	live := httptest.NewServer(realHandler)
	defer live.Close()
	result.Fixed = b.fixedArrival(t, live.URL+b.f.publicPath(), n)
	result.Phases = b.measurePhases(t, min(n, 100))
	verifyBillingCapacity(t, b, money, wallet, &result)
	return result
}

func newBillingCapacity(t *testing.T, source string) (*gatewayBench, *billing.Service, billing.Wallet) {
	t.Helper()
	b := newGatewayBench(t)
	b.billing = true
	charges, err := newGatewayBilling(b.f.database, time.Now)
	require.NoError(t, err)
	a := billing.Actor{TenantID: b.p.TenantID, UserID: b.p.OwnerUserID}
	wallet, err := charges.money.Open(b.f.ctx, a, b.p.AppID)
	require.NoError(t, err)
	wallet, err = charges.money.Topup(b.f.ctx, a, wallet.ID, 1000000000, "capacity fixture", "capacity-funding")
	require.NoError(t, err)
	b.f.route.BillingMode, b.f.route.PriceCents, b.f.route.UnitsPerCall = "per_call", 5, 1
	b.f.route, err = b.f.gate.SaveRoute(b.f.ctx, b.f.gatewayActor(), b.f.route)
	require.NoError(t, err)
	if source == "paid" {
		_, err = charges.money.Grant(b.f.ctx, a, billing.GrantInput{AppID: b.p.AppID, Kind: "paid", QuotaType: "calls", Units: 1000000, SourceRef: "capacity fixture"}, "capacity-grant")
		require.NoError(t, err)
	}
	b.calls, err = gateway.NewCallService(b.f.gate, time.Now, gateway.CallPolicy{Timeout: 5 * time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)}, charges)
	require.NoError(t, err)
	b.open, err = gateway.NewOpenHandler(b.auth, b.f.gate, gatewayStatusFunc(b.snapshot), b.calls, b.limits, time.Now, charges)
	require.NoError(t, err)
	return b, charges.money, wallet
}

func verifyBillingCapacity(t *testing.T, b *gatewayBench, money *billing.Service, wallet billing.Wallet, out *billingCapacityScenario) {
	t.Helper()
	q, cancel := b.f.database.WithContext(b.f.ctx)
	defer cancel()
	var facts struct{ Calls, Meters, Logs, Charges, Cash, Units, Quota int64 }
	require.NoError(t, q.Raw(`SELECT
 (SELECT count(*) FROM api_call WHERE state='succeeded') calls,
 (SELECT count(*) FROM api_call_meter) meters,
 (SELECT count(*) FROM api_call_log WHERE state='succeeded') logs,
 (SELECT count(*) FROM call_charge WHERE state='settled') charges,
 (SELECT coalesce(sum(amount_cents),0) FROM wallet_txn WHERE type='settle') cash,
 (SELECT coalesce(sum(amount),0) FROM unit_txn WHERE type='consume') units,
 (SELECT min(admitted_count) FROM api_quota) quota`).Scan(&facts).Error)
	require.Equal(t, facts.Calls, facts.Meters)
	require.Equal(t, facts.Calls, facts.Logs)
	require.Equal(t, facts.Calls, facts.Charges)
	require.Equal(t, facts.Calls, facts.Quota)
	if out.Source == "wallet" {
		require.Equal(t, facts.Calls*5, facts.Cash)
		require.Zero(t, facts.Units)
	} else {
		require.Zero(t, facts.Cash)
		require.Equal(t, facts.Calls, facts.Units)
	}
	balance, err := money.Balance(b.f.ctx, b.p.TenantID, b.p.AppID)
	require.NoError(t, err)
	require.Equal(t, wallet.BalanceCents-facts.Cash, balance.BalanceCents)
	require.Zero(t, balance.FrozenCents)
	out.Calls, out.CashCents, out.Units = facts.Calls, facts.Cash, facts.Units
}
