package refresh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/modules/shop/refresh/port"
	"github.com/stretchr/testify/require"
)

func TestFailureClassificationProtectsUnknownConsumption(t *testing.T) {
	// REQ-SHP-008: only an explicit non-consumption guarantee permits retries.
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"rate limited", &port.Failure{HTTPStatus: 429, NotConsumed: true}, FailureTemporary},
		{"server rejected", &port.Failure{HTTPStatus: 503, NotConsumed: true}, FailureTemporary},
		{"timeout before sending", &port.Failure{Timeout: true, NotConsumed: true}, FailureTemporary},
		{"ambiguous server error", &port.Failure{HTTPStatus: 503}, FailureUnknown},
		{"response timeout", context.DeadlineExceeded, FailureUnknown},
		{"untyped error", errors.New("transport failed"), FailureUnknown},
		{"explicit revocation", &port.Failure{Revoked: true}, FailureInvalid},
		{"bare unauthorized", &port.Failure{HTTPStatus: 401}, FailureUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, Classify(tc.err)) })
	}
}

func TestRetryPolicyAndLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.Jitter = func(time.Duration) (time.Duration, error) { return 0, nil }
	for i, wait := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour} {
		next, err := cfg.NextRetry(now, i+1, time.Time{})
		require.NoError(t, err)
		require.Equal(t, now.Add(wait), next)
	}
	later := now.Add(24 * time.Hour)
	next, err := cfg.NextRetry(now, 1, later)
	require.NoError(t, err)
	require.Equal(t, later, next)
	_, err = cfg.NextRetry(now, 6, time.Time{})
	require.Error(t, err)
	require.Equal(t, "active", ProjectStatus(now, nil))
	for _, ttl := range []time.Duration{-time.Hour, 0, 72 * time.Hour} {
		expiry := now.Add(ttl)
		require.Equal(t, "expiring", ProjectStatus(now, &expiry))
	}
	expiry := now.Add(73 * time.Hour)
	require.Equal(t, "active", ProjectStatus(now, &expiry))
}
