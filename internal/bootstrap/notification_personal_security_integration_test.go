package bootstrap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

func TestIdentityNotificationPersonalProbeReplacementIntegration(t *testing.T) {
	f := newNotificationFixtureF3(t)
	endpoint := "https://personal.example.invalid/" + personalRandom(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := f.manager.SavePersonalChannel(f.ctx, f.actor, "bark", channel.PersonalChannelInput{Endpoint: endpoint}, nil)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	savePersonalFixture(f.ctx, t, f.manager, f.actor, "bark", server.URL, false)
	result, err := f.manager.TestPersonalChannel(f.ctx, f.actor, "bark", nil)
	require.NoError(t, err)
	var d channel.Delivery
	require.NoError(t, json.Unmarshal(result.Body, &d))
	require.Equal(t, "failed", d.Status)
	require.Equal(t, "recipient_unavailable", d.LastError)
	row, err := f.manager.store.personalChannel(f.ctx, 1, f.user.ID, "bark")
	require.NoError(t, err)
	require.Nil(t, row.VerifiedAt)
	current, err := f.manager.store.openPersonalEndpoint(row)
	require.NoError(t, err)
	require.Equal(t, endpoint, current)
	f.assertPrivacy(t, endpoint)
}

func TestIdentityNotificationPersonalProbeFailureIntegration(t *testing.T) {
	f := newNotificationFixtureF3(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	savePersonalFixture(f.ctx, t, f.manager, f.actor, "webhook", server.URL, false)
	result, err := f.manager.TestPersonalChannel(f.ctx, f.actor, "webhook", nil)
	require.NoError(t, err)
	var d channel.Delivery
	require.NoError(t, json.Unmarshal(result.Body, &d))
	require.Equal(t, "failed", d.Status)
	require.Equal(t, "provider_delivery_failed", d.LastError)
	row, err := f.manager.store.personalChannel(f.ctx, 1, f.user.ID, "webhook")
	require.NoError(t, err)
	require.Nil(t, row.VerifiedAt)
}
