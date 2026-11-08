package main

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestScopedDSNPreventsPublicFallback(t *testing.T) {
	for _, dsn := range []string{"postgres://localhost/example?search_path=public", "host=localhost dbname=example search_path=public"} {
		t.Run(dsn, func(t *testing.T) {
			schema := "s3_smoke_" + strings.Repeat("a", 32)
			t.Setenv("SMOKE_SCHEMA", schema)
			t.Setenv("SMOKE_DATABASE_URL", dsn)
			cfg, err := pgx.ParseConfig(scopedDSN())
			require.NoError(t, err)
			require.Equal(t, schema, cfg.RuntimeParams["search_path"])
			require.Equal(t, schema, cfg.RuntimeParams["application_name"])
		})
	}
}

func TestSchemaRejectsNonOwnedNames(t *testing.T) {
	for _, name := range []string{"public", "", "s3_smoke_", "s3_smoke_" + strings.Repeat("a", 32) + ";drop schema public"} {
		t.Setenv("SMOKE_SCHEMA", name)
		_, err := schemaName()
		require.Error(t, err)
	}
}

func TestRedisReservationAndOwnershipProtectOtherData(t *testing.T) {
	server := miniredis.RunT(t)
	t.Setenv("CH_REDIS_ADDR", server.Addr())
	t.Setenv("CH_REDIS_PASSWORD", "")
	t.Setenv("CH_REDIS_DB", "1")
	t.Setenv("SMOKE_SCHEMA", "s3_smoke_"+strings.Repeat("b", 32))
	ctx := context.Background()
	other := redis.NewClient(redisOptions(2))
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	require.NoError(t, other.Set(ctx, "development", "preserved", 0).Err())
	result, err := reserveRedis(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, result.(map[string]any)["db"])
	t.Setenv("CH_REDIS_DB", "3")
	owned := redis.NewClient(redisOptions(3))
	t.Cleanup(func() { require.NoError(t, owned.Close()) })
	require.NoError(t, owned.Set(ctx, "asynq:fixture", "sample", 0).Err())
	t.Setenv("SMOKE_SCHEMA", "s3_smoke_"+strings.Repeat("c", 32))
	_, err = cleanRedis(ctx)
	require.Error(t, err)
	require.Equal(t, int64(2), owned.DBSize(ctx).Val())
	t.Setenv("SMOKE_SCHEMA", "s3_smoke_"+strings.Repeat("b", 32))
	result, err = cleanRedis(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(0), result.(map[string]any)["remaining_keys"])
	require.Equal(t, "preserved", other.Get(ctx, "development").Val())
}
