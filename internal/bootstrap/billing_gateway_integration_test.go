package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestBillingGatewayIntegrationConcurrentAdmission(t *testing.T) {
	// REQ-BILL-002: insufficient funds never reach the business reader or reserve quota.
	f := newBillingHTTPFixture(t, 100)
	var executed atomic.Int64
	reader := gatewayStatusFunc(func(context.Context, gateway.Principal, string) (gateway.OpenOrderStatus, error) {
		executed.Add(1)
		return gateway.OpenOrderStatus{ID: f.grant.PublicID, Status: "completed", UpdatedAt: f.now}, nil
	})
	first, second := f.open(t, reader), f.open(t, reader)
	requests := make([]*http.Request, 30)
	for i := range requests {
		requests[i] = f.signed(t, "GET", f.publicPath(), "concurrent-"+strconv.Itoa(i), nil)
	}
	start := make(chan struct{})
	out := make(chan *httptest.ResponseRecorder, 30)
	var wg sync.WaitGroup
	for i, r := range requests {
		handler := []http.Handler{first, second}[i%2]
		wg.Add(1)
		go func() { defer wg.Done(); <-start; w := httptest.NewRecorder(); handler.ServeHTTP(w, r); out <- w }()
	}
	close(start)
	wg.Wait()
	close(out)
	var succeeded, denied int
	for w := range out {
		if w.Code == 200 {
			succeeded++
			continue
		}
		require.Equal(t, 402, w.Code, w.Body.String())
		var body struct{ Code int }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, 90102, body.Code)
		denied++
	}
	require.Equal(t, 20, succeeded)
	require.Equal(t, 10, denied)
	require.EqualValues(t, 20, executed.Load())
	f.assertFunds(t, 0, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var amount, meters, charges, quota int64
	require.NoError(t, q.Table("api_call_log").Select("sum(cost_cents)").Scan(&amount).Error)
	require.EqualValues(t, 100, amount)
	require.NoError(t, q.Table("api_call_meter").Count(&meters).Error)
	require.EqualValues(t, 20, meters)
	require.NoError(t, q.Table("call_charge").Count(&charges).Error)
	require.EqualValues(t, 20, charges)
	require.NoError(t, q.Table("api_quota").Select("min(admitted_count)").Scan(&quota).Error)
	require.EqualValues(t, 20, quota)
}

func TestBillingGatewayIntegrationFailureAndCrashRecovery(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	f.handler = f.open(t, gatewayStatusFunc(func(context.Context, gateway.Principal, string) (gateway.OpenOrderStatus, error) {
		return gateway.OpenOrderStatus{}, errs.New(80301, 502, "injected upstream failure")
	}))
	w, _ := f.serve(t, f.signed(t, "GET", f.publicPath(), "failed", nil))
	require.Equal(t, 502, w.Code)
	f.assertFunds(t, 100, 0)
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	f.assertFunds(t, 100, 5)
	// A new worker after the process died uses the same terminal transaction and cash port.
	recovery, err := newGatewayReconciler(f.database, func() time.Time { return c.DeadlineAt.Add(time.Second) })
	require.NoError(t, err)
	count, err := recovery.Reconcile(f.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	f.assertFunds(t, 100, 0)
	charge, err := (&billingStore{database: f.database}).Charge(f.ctx, 1, c.ID)
	require.NoError(t, err)
	require.Equal(t, "expired", charge.State)
	require.Error(t, f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)}))
	f.assertFunds(t, 100, 0)
	count, err = recovery.Reconcile(f.ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestBillingGatewayIntegrationAtomicMeterAndLostCommit(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	policy := gateway.CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)}
	failed, err := gateway.NewCallService(&gatewayMeterFailure{f.gate}, func() time.Time { return f.now }, policy, f.charges)
	require.NoError(t, err)
	c := f.intent()
	require.NoError(t, failed.Start(f.ctx, c))
	result := gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)}
	require.Error(t, failed.Finish(f.ctx, c.ID, result))
	f.assertFunds(t, 100, 5)
	gatewayAssertCall(t, f.gatewayHTTPFixture, c.ID, "processing", 0, 0)
	charge, err := (&billingStore{database: f.database}).Charge(f.ctx, 1, c.ID)
	require.NoError(t, err)
	require.Equal(t, "reserved", charge.State)
	require.NoError(t, f.calls.Finish(f.ctx, c.ID, result))
	require.NoError(t, f.calls.Finish(f.ctx, c.ID, result))
	f.assertFunds(t, 95, 0)
	lost := &gatewayLostCommit{gatewayStore: f.gate, lose: true}
	svc, err := gateway.NewCallService(lost, func() time.Time { return f.now }, policy, f.charges)
	require.NoError(t, err)
	c = f.intent()
	require.NoError(t, svc.Start(f.ctx, c))
	lost.lose = true
	require.NoError(t, svc.Finish(f.ctx, c.ID, result))
	f.assertFunds(t, 90, 0)
	gatewayAssertCall(t, f.gatewayHTTPFixture, c.ID, "succeeded", 1, 1)
}

func TestBillingGatewayIntegrationRecoveryRacesLateFinish(t *testing.T) {
	f := newBillingHTTPFixture(t, 100)
	c := f.intent()
	require.NoError(t, f.calls.Start(f.ctx, c))
	recovery, err := newGatewayReconciler(f.database, func() time.Time { return c.DeadlineAt.Add(time.Second) })
	require.NoError(t, err)
	start := make(chan struct{})
	out := make(chan error, 2)
	go func() {
		<-start
		out <- f.calls.Finish(f.ctx, c.ID, gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)})
	}()
	go func() { <-start; _, err := recovery.Reconcile(f.ctx); out <- err }()
	close(start)
	first, second := <-out, <-out
	require.True(t, first == nil || second == nil)
	call, err := f.gate.Call(f.ctx, c.ID)
	require.NoError(t, err)
	if call.State == "succeeded" {
		f.assertFunds(t, 95, 0)
		gatewayAssertCall(t, f.gatewayHTTPFixture, c.ID, "succeeded", 1, 1)
	} else {
		require.Equal(t, "failed", call.State)
		f.assertFunds(t, 100, 0)
		gatewayAssertCall(t, f.gatewayHTTPFixture, c.ID, "failed", 0, 0)
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var terminal int64
	require.NoError(t, q.Table("wallet_txn").Where("call_id=? AND type IN ('settle','release')", c.ID).Count(&terminal).Error)
	require.EqualValues(t, 1, terminal)
}

func TestBillingGatewayIntegrationUnavailableChargePort(t *testing.T) {
	// REQ-BILL-002: unavailable billing must not silently execute a paid call.
	f := newBillingHTTPFixture(t, 100)
	var err error
	f.calls, err = gateway.NewCallService(f.gate, func() time.Time { return f.now },
		gateway.CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)})
	require.NoError(t, err)
	var executed atomic.Int64
	f.handler = f.open(t, gatewayStatusFunc(func(context.Context, gateway.Principal, string) (gateway.OpenOrderStatus, error) {
		executed.Add(1)
		return gateway.OpenOrderStatus{}, nil
	}))
	w, body := f.serve(t, f.signed(t, "GET", f.publicPath(), "billing-unavailable", nil))
	require.Equal(t, 503, w.Code, w.Body.String())
	var code int
	require.NoError(t, json.Unmarshal(body["code"], &code))
	require.Equal(t, 10002, code)
	require.Zero(t, executed.Load())
	f.assertFunds(t, 100, 0)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"call_charge", "api_call_meter", "api_quota"} {
		var count int64
		require.NoError(t, q.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
}
