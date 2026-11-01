package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionRevocationIndexIntegrationUpAndDown(t *testing.T) {
	database, ctx := integrationDB(t)
	state, err := database.Migrate(ctx, "up")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 4}, state)
	var definition string
	err = database.handle.WithContext(ctx).Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&definition).Error
	require.NoError(t, err)
	require.Contains(t, definition, "(tenant_id, session_id, user_id)")
	for _, reason := range []string{"logout", "kick", "replay"} {
		require.Contains(t, definition, reason)
	}
	require.Contains(t, definition, "WHERE")
	t.Log("up -> 4: idx_refresh_session_revoked exists")
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 3}, state)
	var afterShopRollback string
	err = database.handle.WithContext(ctx).Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&afterShopRollback).Error
	require.NoError(t, err)
	require.Equal(t, definition, afterShopRollback)
	t.Log("down -> 3: idx_refresh_session_revoked exists, definition unchanged")
	state, err = database.Migrate(ctx, "down")
	require.NoError(t, err)
	require.Equal(t, MigrationVersion{Version: 2}, state)
	var count int64
	err = database.handle.WithContext(ctx).Raw("SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = 'idx_refresh_session_revoked'").Scan(&count).Error
	require.NoError(t, err)
	require.Zero(t, count)
	t.Log("down -> 2: idx_refresh_session_revoked absent")
}

func TestMigrationGateIntegrationWaitHonorsDeadline(t *testing.T) {
	database, ctx := integrationDB(t)
	pool, err := database.handle.DB()
	require.NoError(t, err)
	first, err := pool.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Close()) }()
	unlock, err := acquireMigrationGate(ctx, first)
	require.NoError(t, err)
	defer func() { require.NoError(t, unlock()) }()
	waiter, err := pool.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, waiter.Close()) }()
	deadline, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = acquireMigrationGate(deadline, waiter)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
