package channel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersonalEndpointPolicy(t *testing.T) {
	ctx := context.Background()
	policy := &PersonalTransport{LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}}
	for _, raw := range []string{
		"http://example.invalid/push", "https://127.0.0.1/push", "https://10.1.2.3/push",
		"https://169.254.169.254/", "https://[::1]/", "https://[::ffff:127.0.0.1]/",
		"https://[fc00::1]/", "https://100.64.0.1/", "https://0.0.0.0/",
		"https://user:pass@example.invalid/", "https://example.invalid/#fragment",
		"https://example.invalid/" + strings.Repeat("a", 2048), "https://localhost/",
	} {
		err := policy.Validate(ctx, "webhook", raw)
		require.Error(t, err)
		require.NotContains(t, err.Error(), raw)
	}
	for _, kind := range []string{"webhook", "bark", "gotify"} {
		require.NoError(t, policy.Validate(ctx, kind, "https://example.invalid/push"))
	}
	policy.AllowedHosts = map[string][]string{"bark": {"api.day.app"}}
	require.Error(t, policy.Validate(ctx, "bark", "https://example.invalid/push"))
	require.NoError(t, policy.Validate(ctx, "bark", "https://api.day.app/push"))
	policy.LookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("192.168.1.1")}}, nil
	}
	require.Error(t, policy.Validate(ctx, "webhook", "https://example.invalid/push"))
	policy.LookupIP = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("resolver failed") }
	require.Error(t, policy.Validate(ctx, "webhook", "https://example.invalid/push"))
}

func TestPersonalEndpointMaskAndScope(t *testing.T) {
	for _, raw := range []string{"https://example.invalid/private/path?token=private", "https://example.invalid/"} {
		masked := MaskRecipient("bark", raw)
		require.Equal(t, "https://example.invalid/…/****", masked)
		require.NotContains(t, masked, "private")
		require.NotEqual(t, raw, masked)
	}
	require.Equal(t, "system", ProviderScope("wecom"))
	require.Equal(t, "system", ProviderScope("dingtalk"))
	require.Equal(t, "personal", ProviderScope("email"))
	require.Equal(t, "personal", ProviderScope("sms"))
}

func TestPersonalTransportRejectsDNSRebinding(t *testing.T) {
	var lookups atomic.Int32
	p := &PersonalTransport{LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}}
	endpoint := "https://example.invalid/" + strings.Repeat("a", 32)
	err := p.Send(context.Background(), "bark", endpoint, Message{Subject: "test", Body: "test"})
	require.Error(t, err)
	require.NotContains(t, err.Error(), endpoint)
	require.EqualValues(t, 2, lookups.Load())
}

func TestPersonalTransportRejectsRedirects(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "https://127.0.0.1/", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	p := &PersonalTransport{LookupIP: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}, Transport: personalRedirectTransport{destination: server.URL}}
	err := p.Send(context.Background(), "webhook", "https://example.invalid/push", Message{Subject: "test", Body: "test"})
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load())
}

type personalRedirectTransport struct{ destination string }

func (p personalRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	local, err := http.NewRequestWithContext(r.Context(), r.Method, p.destination, r.Body)
	if err != nil {
		return nil, err
	}
	return http.DefaultTransport.RoundTrip(local)
}
