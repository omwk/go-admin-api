package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTenantLoginFailureCountersAndWindows(t *testing.T) {
	// BE-IDN-001: account and tenant windows expire independently and never extend.
	r, server := revocationRedis(t)
	ctx := context.Background()
	require.NoError(t, r.RecordLoginFailure(ctx, "acme", "operator", 15*time.Minute))
	require.Equal(t, 15*time.Minute, server.TTL("login:fail:acme:operator"))
	require.Equal(t, time.Minute, server.TTL("login:rate:acme"))
	server.FastForward(30 * time.Second)
	require.NoError(t, r.RecordLoginFailure(ctx, "acme", "operator", 15*time.Minute))
	require.Equal(t, 14*time.Minute+30*time.Second, server.TTL("login:fail:acme:operator"))
	require.Equal(t, 30*time.Second, server.TTL("login:rate:acme"))
	require.NoError(t, r.RecordLoginFailure(ctx, "default", "operator", 15*time.Minute))
	require.NoError(t, r.ClearLoginFailures(ctx, "acme", "operator"))
	require.False(t, server.Exists("login:fail:acme:operator"))
	require.True(t, server.Exists("login:fail:default:operator"))
	value, err := server.Get("login:rate:acme")
	require.NoError(t, err)
	require.Equal(t, "2", value, "success must not clear the tenant failure window")
	for i := 2; i < tenantLoginFailureLimit; i++ {
		require.NoError(t, r.RecordLoginFailure(ctx, "acme", fmt.Sprintf("account-%d", i), 15*time.Minute))
	}
	allowed, err := r.AllowTenantLogin(ctx, "acme")
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = r.AllowTenantLogin(ctx, "default")
	require.NoError(t, err)
	require.True(t, allowed)
	server.FastForward(30 * time.Second)
	allowed, err = r.AllowTenantLogin(ctx, "acme")
	require.NoError(t, err)
	require.True(t, allowed)
	t.Log("100 failures => tenant limited; account TTL=15m; tenant TTL=1m; success clears only its account key; windows do not slide")
}

func TestTenantLoginFailureCounterAtomicityAndKeyIsolation(t *testing.T) {
	r, server := revocationRedis(t)
	ctx := context.Background()
	var workers sync.WaitGroup
	results := make(chan error, 32)
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- r.RecordLoginFailure(ctx, "acme", "operator", 15*time.Minute)
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	for _, key := range []string{"login:fail:acme:operator", "login:rate:acme"} {
		value, err := server.Get(key)
		require.NoError(t, err)
		require.Equal(t, "32", value)
	}
	require.NotEqual(t, loginFailureKey("acme:division", "operator"), loginFailureKey("acme", "division:operator"))
	require.NoError(t, r.RecordLoginFailure(ctx, "acme:division", "operator", time.Minute))
	require.NoError(t, r.RecordLoginFailure(ctx, "acme", "division:operator", time.Minute))
	for _, key := range []string{loginFailureKey("acme:division", "operator"), loginFailureKey("acme", "division:operator")} {
		value, err := server.Get(key)
		require.NoError(t, err)
		require.Equal(t, "1", value)
	}
}

func TestTenantLoginLimitsFailClosedWithoutLeakingAccount(t *testing.T) {
	r, server := revocationRedis(t)
	ctx := context.Background()
	server.SetError("injected Redis failure")
	_, err := r.AllowTenantLogin(ctx, "acme")
	require.Error(t, err)
	require.Error(t, r.RecordLoginFailure(ctx, "acme", "private-account", time.Minute))
	err = r.ClearLoginFailures(ctx, "acme", "private-account")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-account")
}
