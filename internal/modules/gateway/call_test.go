package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestCallExecutionLogicalIdentityAndFailures(t *testing.T) {
	// REQ-GW-007: only a complete durable success meters; internal retries keep call_id.
	for _, tc := range []struct {
		name              string
		failure           error
		cancel, panicCall bool
		attempts, units   int
	}{
		{name: "success", attempts: 1, units: 1},
		{name: "parameter failure", failure: Invalid(), attempts: 1},
		{name: "upstream 5xx", failure: errs.New(60403, 502, "upstream failure"), attempts: 2},
		{name: "mapped upstream failure", failure: errs.New(80301, 502, "upstream failure"), attempts: 2},
		{name: "upstream unavailable", failure: errs.New(80301, 503, "upstream unavailable"), attempts: 1},
		{name: "timeout", failure: context.DeadlineExceeded, attempts: 1},
		{name: "client cancellation before persistence", cancel: true, attempts: 1},
		{name: "panic", panicCall: true, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newCallTestService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			call := svc.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Version: 1, Method: "GET"}, "trace", "request", "192.0.2.1")
			var seen []string
			_, err := svc.Run(ctx, call, func(work context.Context) (OpenOrderStatus, error) {
				seen = append(seen, CallID(work))
				if tc.cancel {
					cancel()
				}
				if tc.panicCall {
					panic("injected execution failure")
				}
				return OpenOrderStatus{ID: "public-order", Status: "completed"}, tc.failure
			})
			if tc.units == 1 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Len(t, store.calls, 1)
			require.Equal(t, tc.attempts, store.calls[call.ID].Attempts)
			require.Equal(t, tc.units, store.calls[call.ID].MeteredUnits)
			require.Equal(t, tc.units, store.meters[call.ID])
			for _, id := range seen {
				require.Equal(t, call.ID, id)
			}
			require.Equal(t, 1, store.admissions)
		})
	}
}

func TestCallRetrySuccessAndUnknownCommit(t *testing.T) {
	svc, store := newCallTestService(t)
	call := svc.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Version: 1, Method: "GET"}, "trace", "same-observation", "192.0.2.1")
	store.loseCommit = true
	var attempts int
	data, err := svc.Run(context.Background(), call, func(ctx context.Context) (OpenOrderStatus, error) {
		require.Equal(t, call.ID, CallID(ctx))
		attempts++
		if attempts == 1 {
			return OpenOrderStatus{}, errs.New(60403, 502, "unavailable")
		}
		store.loseCommit = true
		return OpenOrderStatus{ID: "public-order", Status: "completed"}, nil
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Equal(t, 2, attempts)
	require.Equal(t, 1, store.meters[call.ID])
	require.Equal(t, 1, store.admissions)
	second := svc.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Version: 1, Method: "GET"}, "trace", "same-observation", "192.0.2.1")
	require.NotEqual(t, call.ID, second.ID)
}

func TestCallTerminalTransactionFailureAndValidation(t *testing.T) {
	svc, store := newCallTestService(t)
	call := svc.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Version: 1, Method: "GET"}, "trace", "request", "192.0.2.1")
	require.NoError(t, svc.Start(context.Background(), call))
	store.meterError = errors.New("injected meter failure")
	result := CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)}
	require.Error(t, svc.Finish(context.Background(), call.ID, result))
	require.Equal(t, "processing", store.calls[call.ID].State)
	require.Empty(t, store.meters)
	store.meterError = nil
	require.NoError(t, svc.Finish(context.Background(), call.ID, result))
	require.NoError(t, svc.Finish(context.Background(), call.ID, result))
	require.Equal(t, 1, store.meters[call.ID])
	require.Error(t, svc.Finish(context.Background(), call.ID, CallResult{StatusCode: 200, Data: json.RawMessage(`null`)}))
}

func TestCallReconcilerWithoutObservationKey(t *testing.T) {
	svc, store := newCallTestService(t)
	call := svc.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Version: 1, Method: "GET"}, "trace", "request", "192.0.2.1")
	require.NoError(t, svc.Start(context.Background(), call))
	reconciler, err := NewCallReconciler(store, func() time.Time { return call.DeadlineAt.Add(time.Second) })
	require.NoError(t, err)
	count, err := reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, "failed", store.calls[call.ID].State)
	require.Zero(t, store.calls[call.ID].MeteredUnits)
	require.Empty(t, store.meters)
	count, err = reconciler.Reconcile(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}
