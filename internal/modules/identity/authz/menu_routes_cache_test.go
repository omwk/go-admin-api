package authz

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMenuProjectionCacheSkipsLegacyPayload(t *testing.T) {
	c, backend, server := versionCache(t)
	ctx := context.Background()
	version, _, err := backend.Version(ctx, VersionKey(1, 2))
	require.NoError(t, err)
	oldKey := fmt.Sprintf("authz:menu:1:2:v%s", version)
	require.NoError(t, server.Set(oldKey, fmt.Sprintf("{\"version\":\"%s\",\"data\":[{\"id\":1}]}", version)))
	expected := []byte("[{\"visible\":true,\"children\":[{\"visible\":false}]}]")
	data, err := c.Load(ctx, 1, 2, "menu", func(context.Context) ([]byte, error) { return expected, nil })
	require.NoError(t, err)
	require.JSONEq(t, string(expected), string(data))
	data, err = c.Load(ctx, 1, 2, "menu", func(context.Context) ([]byte, error) { return nil, errors.New("unexpected cache miss") })
	require.NoError(t, err)
	require.JSONEq(t, string(expected), string(data))
	require.True(t, server.Exists(oldKey), "legacy entries expire naturally without changing other authorization payloads")
}
