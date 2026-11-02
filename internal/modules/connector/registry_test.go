package connector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type testPlatform struct{ code string }

func (p testPlatform) Code() string             { return p.code }
func (testPlatform) Capabilities() Capabilities { return Capabilities{} }
func (testPlatform) Fetch(context.Context, Credential, FetchRequest) (FetchResult, error) {
	return FetchResult{}, nil
}

func TestRegistryRejectsDuplicatesAndSortsCodes(t *testing.T) {
	registry := NewRegistry()
	require.Error(t, registry.Register(nil))
	require.NoError(t, registry.Register(testPlatform{code: "z"}))
	require.NoError(t, registry.Register(testPlatform{code: "a"}))
	require.Error(t, registry.Register(testPlatform{code: "a"}))
	require.Equal(t, []string{"a", "z"}, registry.List())
}

func TestPlatformErrorPreservesKindAndStatus(t *testing.T) {
	err := &PlatformError{Kind: ErrRateLimited, Status: 429, RetryAfterSeconds: 60}
	require.Equal(t, ErrRateLimited, err.Kind)
	require.Equal(t, 429, err.Status)
	require.Contains(t, err.Error(), "rate_limited")
}
