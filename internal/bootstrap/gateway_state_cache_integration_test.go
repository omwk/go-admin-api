package bootstrap

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGatewayStateIntegrationCacheVersionsExpiryAndFailure(t *testing.T) {
	f := newGatewayHTTPFixture(t)
	now := time.Now()
	f.gate.state.clock = func() time.Time { return now }
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var queries atomic.Int64
	require.NoError(t, q.Callback().Query().Before("gorm:query").Register("m9c:state", func(*gorm.DB) { queries.Add(1) }))
	read := func(ctx context.Context) error {
		c, err := f.gate.FindClient(ctx, f.client.AppID)
		if err != nil {
			return err
		}
		_, err = f.gate.Secrets(ctx, c.TenantID, c.ID)
		if err != nil {
			return err
		}
		_, err = f.gate.Route(ctx, gateway.Principal{TenantID: c.TenantID}, f.route.Code)
		return err
	}
	require.NoError(t, read(f.ctx))
	require.EqualValues(t, 6, queries.Swap(0))
	require.NoError(t, read(f.ctx))
	require.EqualValues(t, 3, queries.Swap(0), "hits read primary versions, not cached authority")
	now = now.Add(5 * time.Second)
	require.NoError(t, read(f.ctx))
	require.EqualValues(t, 6, queries.Swap(0), "five-second snapshots expire")
	other := &gatewayStore{database: f.database}
	route := f.route
	route.PriceCents = 17
	_, err := other.SaveRoute(f.ctx, f.gatewayActor(), route)
	require.NoError(t, err)
	got, err := f.gate.Route(f.ctx, gateway.Principal{TenantID: f.client.TenantID}, f.route.Code)
	require.NoError(t, err)
	require.EqualValues(t, 17, got.PriceCents, "another instance's version invalidates cached prices")
	ctx, stop := context.WithCancel(f.ctx)
	stop()
	require.Error(t, read(ctx), "cached authority fails closed when primary read fails")
}

func TestGatewayStateIntegrationOtherInstanceRevocation(t *testing.T) {
	f := newGatewayFixture(t)
	_, err := f.auth.Authenticate(f.ctx, f.signed(t, f.issued.Secret))
	require.NoError(t, err)
	other, err := gateway.NewService(&gatewayStore{database: f.database}, f.sealer, func() time.Time { return f.now })
	require.NoError(t, err)
	require.NoError(t, other.Revoke(f.ctx, f.actor(), f.client.ID, f.issued.ID))
	_, err = f.auth.Authenticate(f.ctx, f.signed(t, f.issued.Secret))
	require.Error(t, err, "a five-second cache must not admit a revoked key")
}
