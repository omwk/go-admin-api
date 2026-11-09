package bootstrap

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestCredentialNotificationNormalMaintenanceAndEscalation(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(10 * time.Minute)
	due := now
	row := credentialObservation{Status: "expiring", Kind: "oauth2", RefreshStrategy: "auto", ExpiresAt: &expiry, RefreshAt: &due, RetryBudget: 5}
	template, _ := notificationLevel(row, now)
	require.Empty(t, template)
	row.RefreshStrategy = "manual"
	template, level := notificationLevel(row, now)
	require.Equal(t, "credential.expiring.1d", template)
	require.Equal(t, "warning", level)
	row.RefreshStrategy = "auto"
	row.Unknown = true
	template, level = notificationLevel(row, now)
	require.Equal(t, "credential.unknown", template)
	require.Equal(t, "critical", level)
	row.Unknown = false
	row.ConsecutiveFailures = 3
	template, _ = notificationLevel(row, now)
	require.Equal(t, "credential.failed", template)
	row.ConsecutiveFailures = 0
	stuck := now.Add(-121 * time.Second)
	row.RunningAt = &stuck
	template, level = notificationLevel(row, now)
	require.Equal(t, "credential.stuck", template)
	require.Equal(t, "critical", level)
}

func TestCredentialNotificationManualDeadlineBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		remaining time.Duration
		template  string
	}{
		{73 * time.Hour, ""}, {72 * time.Hour, "credential.expiring.3d"},
		{24 * time.Hour, "credential.expiring.1d"}, {0, "credential.expired"},
	} {
		expiry := now.Add(test.remaining)
		row := credentialObservation{Kind: "cookie", RefreshStrategy: "manual", ExpiresAt: &expiry, RetryBudget: 5}
		template, _ := notificationLevel(row, now)
		require.Equal(t, test.template, template)
	}
	row := credentialObservation{Kind: "oauth2", RefreshStrategy: "auto", RetryBudget: 5}
	template, _ := notificationLevel(row, now)
	require.Equal(t, "credential.protocol_pending", template)
}
