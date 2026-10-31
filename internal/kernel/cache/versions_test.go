package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVersionPipelineRecordsPartialFailure(t *testing.T) {
	c, server := testCache(t)
	ctx := context.Background()
	require.NoError(t, server.Set("bad-version", "not-an-integer"))
	results := c.IncrBatch(ctx, []string{"good-version", "bad-version"})
	require.Len(t, results, 2)
	require.NoError(t, results[0].Err)
	require.Error(t, results[1].Err)
	require.Greater(t, results[0].Version, int64(1))
	require.Zero(t, server.TTL("good-version"))
	_, _, err := c.Version(ctx, "bad-version")
	require.Error(t, err)
}
