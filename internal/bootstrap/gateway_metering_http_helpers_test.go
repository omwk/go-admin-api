package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

type gatewayStatusFunc func(context.Context, gateway.Principal, string) (gateway.OpenOrderStatus, error)

func (f gatewayStatusFunc) OrderStatus(ctx context.Context, p gateway.Principal, id string) (gateway.OpenOrderStatus, error) {
	return f(ctx, p, id)
}

func gatewayMeteredHTTP(t *testing.T, f *gatewayHTTPFixture, reader gateway.OrderStatusReader, limits gateway.RateLimiter, services ...*gateway.CallService) http.Handler {
	t.Helper()
	nonces, err := newGatewayNonces(f.ctx, config.Redis{Addr: f.server.Addr(), PoolSize: 4})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nonces.Close()) })
	auth, err := gateway.NewAuthenticator(f.gate, nonces, f.sealer, func(*http.Request) string { return "192.0.2.1" }, func() time.Time { return f.now })
	require.NoError(t, err)
	if reader == nil {
		reader, err = NewGatewayOrderReader(f.database)
		require.NoError(t, err)
	}
	if limits == nil {
		limits, err = newGatewayLimiter(nonces.client, config.DefaultGateway())
		require.NoError(t, err)
	}
	calls := gatewayCalls(t, f)
	if len(services) > 0 {
		calls = services[0]
	}
	open, err := gateway.NewOpenHandler(auth, f.gate, reader, calls, limits, func() time.Time { return f.now })
	require.NoError(t, err)
	return open
}

type gatewayDisconnectWriter struct {
	*httptest.ResponseRecorder
	beforeWrite func()
}

func (w *gatewayDisconnectWriter) Write([]byte) (int, error) {
	w.beforeWrite()
	return 0, errors.New("client disconnected")
}

type gatewayUnavailableLimiter struct{}

func (gatewayUnavailableLimiter) Take(context.Context, gateway.Principal, int64) (gateway.RateDecision, error) {
	return gateway.RateDecision{}, gateway.Unavailable()
}

func gatewayAssertCall(t *testing.T, f *gatewayHTTPFixture, id, state string, attempts, units int) {
	t.Helper()
	call, err := f.gate.Call(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, state, call.State)
	require.Equal(t, attempts, call.Attempts)
	require.Equal(t, units, call.MeteredUnits)
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("api_call_meter").Where("call_id=?", id).Count(&count).Error)
	require.EqualValues(t, units, count)
	var log struct {
		State                  string
		Attempts, MeteredUnits int
	}
	require.NoError(t, q.Table("api_call_log").Select("state,attempts,metered_units").Where("call_id=? AND called_at=?", id, call.CalledAt).Take(&log).Error)
	require.Equal(t, state, log.State)
	require.Equal(t, attempts, log.Attempts)
	require.Equal(t, units, log.MeteredUnits)
}
