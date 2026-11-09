package bootstrap

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestGatewayMeteringHTTPIntegrationRetriesKeepOneIntent(t *testing.T) {
	// REQ-GW-007: the committed intent is visible before the first business attempt.
	f := newGatewayHTTPFixture(t)
	reader, err := NewGatewayOrderReader(f.database)
	require.NoError(t, err)
	var ids []string
	f.handler = gatewayMeteredHTTP(t, f, gatewayStatusFunc(func(ctx context.Context, p gateway.Principal, id string) (gateway.OpenOrderStatus, error) {
		require.False(t, db.InTransaction(ctx), "business work must execute outside the admission transaction")
		ids = append(ids, gateway.CallID(ctx))
		call, err := f.gate.Call(ctx, ids[len(ids)-1])
		require.NoError(t, err)
		require.Equal(t, "processing", call.State)
		if len(ids) == 1 {
			return gateway.OpenOrderStatus{}, errs.New(60403, 502, "injected upstream failure")
		}
		return reader.OrderStatus(ctx, p, id)
	}), nil)
	w, _ := f.serve(t, f.sign(t, f.publicPath(), f.key.Secret))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Len(t, ids, 2)
	require.Equal(t, ids[0], ids[1])
	require.Equal(t, ids[0], w.Header().Get("X-Call-Id"))
	gatewayAssertCall(t, f, ids[0], "succeeded", 2, 1)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("api_call_log").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestGatewayMeteringHTTPIntegrationFailuresNeverMeter(t *testing.T) {
	for _, tc := range []struct {
		name              string
		failure           error
		cancel, panicCall bool
		status, attempts  int
	}{
		{name: "business failure", failure: gateway.ResourceUnavailable(), status: 404, attempts: 1},
		{name: "upstream 5xx", failure: errs.New(60403, 502, "injected failure"), status: 502, attempts: 2},
		{name: "timeout", failure: context.DeadlineExceeded, status: 504, attempts: 1},
		{name: "cancel before persistence", cancel: true, status: 504, attempts: 1},
		{name: "panic", panicCall: true, status: 500, attempts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayHTTPFixture(t)
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			f.handler = gatewayMeteredHTTP(t, f, gatewayStatusFunc(func(context.Context, gateway.Principal, string) (gateway.OpenOrderStatus, error) {
				if tc.cancel {
					cancel()
				}
				if tc.panicCall {
					panic("injected execution failure")
				}
				return gateway.OpenOrderStatus{}, tc.failure
			}), nil)
			r := f.sign(t, f.publicPath(), f.key.Secret).WithContext(ctx)
			w, body := f.serve(t, r)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.NotContains(t, body, "data")
			gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "failed", tc.attempts, 0)
		})
	}
}

func TestGatewayMeteringHTTPIntegrationDisconnectAfterCommitStillMeters(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	w := &gatewayDisconnectWriter{ResponseRecorder: httptest.NewRecorder()}
	w.beforeWrite = func() {
		gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "succeeded", 1, 1)
		cancel()
	}
	f.handler.ServeHTTP(w, f.sign(t, f.publicPath(), f.key.Secret).WithContext(ctx))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Equal(t, 200, w.Code)
	gatewayAssertCall(t, f, w.Header().Get("X-Call-Id"), "succeeded", 1, 1)
}
