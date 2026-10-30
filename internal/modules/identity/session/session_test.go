package session

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/cache"
	"github.com/omwk/go-admin-api/internal/kernel/config"
	"github.com/omwk/go-admin-api/internal/kernel/idgen"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/stretchr/testify/require"
)

func TestNewIDAndRevocationKeyNamespaces(t *testing.T) {
	sessionID, err := NewID()
	require.NoError(t, err)
	_, err = ulid.ParseStrict(sessionID)
	require.NoError(t, err)
	nextID, err := idgen.NewULID()
	require.NoError(t, err)
	require.NotEqual(t, sessionID, nextID)
	refreshToken, err := refresh.Generate()
	require.NoError(t, err)
	tokenHash := refresh.Hash(refreshToken)

	// REQ-IAM-010: equal suffixes must still use independent ADR-012 namespaces.
	keys := []string{AccessKey(tokenHash), RefreshKey(tokenHash), RevocationKey(tokenHash)}
	require.Equal(t, "auth:v1:revoked:access:jti:"+tokenHash, keys[0])
	require.Equal(t, "auth:v1:revoked:refresh:sha256:"+tokenHash, keys[1])
	require.Equal(t, "auth:v1:revoked:session:"+tokenHash, keys[2])
	if keys[0] == keys[1] || keys[0] == keys[2] || keys[1] == keys[2] {
		t.Fatal("revocation key namespaces must be independent")
	}

	server := miniredis.RunT(t)
	client, err := cache.Open(context.Background(), config.Redis{Addr: server.Addr(), PoolSize: 2, MinIdleConns: 1})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ttls := []time.Duration{15 * time.Minute, 7 * 24 * time.Hour, 6 * 24 * time.Hour}
	for i, key := range keys {
		require.NoError(t, client.Set(context.Background(), key, string(rune('a'+i)), ttls[i]))
	}
	for i, key := range keys {
		value, exists, getErr := client.Get(context.Background(), key)
		require.NoError(t, getErr)
		require.True(t, exists)
		require.Equal(t, string(rune('a'+i)), value)
		require.Equal(t, ttls[i], server.TTL(key))
	}
}

func TestRemainingTTL(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	require.Equal(t, 30*time.Second, RemainingTTL(now.Add(30*time.Second), now))
	require.Zero(t, RemainingTTL(now, now))
	require.Zero(t, RemainingTTL(now.Add(-time.Second), now))
}
