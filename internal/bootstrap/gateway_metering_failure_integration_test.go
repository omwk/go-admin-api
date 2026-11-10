package bootstrap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

type gatewayMeterFailure struct{ *gatewayStore }

func (*gatewayMeterFailure) InsertMeter(context.Context, gateway.Call) error {
	return gateway.Unavailable()
}

type gatewayLostCommit struct {
	*gatewayStore
	lose bool
}

func (s *gatewayLostCommit) Transaction(ctx context.Context, fn func(context.Context) error) error {
	err := s.gatewayStore.Transaction(ctx, fn)
	if err == nil && s.lose {
		s.lose = false
		return gateway.Unavailable()
	}
	return err
}

func TestGatewayMeteringIntegrationTransactionRollbackAndLostAcknowledgement(t *testing.T) {
	// REQ-GW-007: the log and unique meter never become inconsistent on failed commit.
	f := newGatewayHTTPFixture(t)
	policy := gateway.CallPolicy{Timeout: time.Second, MaxAttempts: 2, HashKey: shopRandomBytes(t, 32)}
	failed, err := gateway.NewCallService(&gatewayMeterFailure{f.gate}, func() time.Time { return f.now }, policy)
	require.NoError(t, err)
	call := gatewayIntent(f, failed)
	require.NoError(t, failed.Start(f.ctx, call))
	result := gateway.CallResult{StatusCode: 200, Attempts: 1, Data: json.RawMessage(`{"status":"completed"}`)}
	require.Error(t, failed.Finish(f.ctx, call.ID, result))
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	for _, table := range []string{"api_call", "api_call_log"} {
		var state string
		require.NoError(t, q.Table(table).Select("state").Where("call_id=?", call.ID).Scan(&state).Error)
		require.Equal(t, "processing", state)
	}
	var meters int64
	require.NoError(t, q.Table("api_call_meter").Count(&meters).Error)
	require.Zero(t, meters)
	lost := &gatewayLostCommit{gatewayStore: f.gate, lose: true}
	svc, err := gateway.NewCallService(lost, func() time.Time { return f.now }, policy)
	require.NoError(t, err)
	second := gatewayIntent(f, svc)
	require.NoError(t, svc.Start(f.ctx, second))
	lost.lose = true
	require.NoError(t, svc.Finish(f.ctx, second.ID, result))
	require.NoError(t, svc.Finish(f.ctx, second.ID, result))
	require.NoError(t, q.Table("api_call_meter").Where("call_id=?", second.ID).Count(&meters).Error)
	require.EqualValues(t, 1, meters)
}

func TestGatewayMeteringIntegrationMigrationUpDownUp(t *testing.T) {
	database, ctx := isolatedIdentityDB(t)
	state := migrationAt(ctx, t, database, 18)
	require.EqualValues(t, 18, state.Version)
	state, err := database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.EqualValues(t, 17, state.Version)
	q, cancel := database.WithContext(ctx)
	defer cancel()
	var absent bool
	require.NoError(t, q.Raw("SELECT to_regclass('api_call') IS NULL AND to_regclass('api_call_meter') IS NULL AND to_regclass('api_quota') IS NULL").Scan(&absent).Error)
	require.True(t, absent)
	state, err = database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.EqualValues(t, latestBootstrapMigration(t), state.Version)
	require.False(t, state.Dirty)
	t.Logf("migration 18 -> 17 -> %d; dirty=false", state.Version)
}
