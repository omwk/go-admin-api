package gateway

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestSigningRequestBindsVersionApplicationAndKey(t *testing.T) {
	// REQ-BILL-003: the payer and logical identity are authenticated v2 fields.
	r := httptest.NewRequest("GET", "/open/v1/balance", nil)
	r.Header.Set("X-App-Id", "application")
	v1, err := CanonicalRequest(r, "123", "nonce", nil)
	require.NoError(t, err)
	plain, err := SigningRequest(r, "123", "nonce", nil)
	require.NoError(t, err)
	require.Equal(t, v1, plain)
	r.Header.Set("X-Signature-Version", "2")
	r.Header.Set("Idempotency-Key", "request-1")
	v2, err := SigningRequest(r, "123", "nonce", nil)
	require.NoError(t, err)
	require.Equal(t, v1+"\nv2\napplication\nrequest-1", v2)
	r.Header.Set("X-App-Id", "other")
	other, err := SigningRequest(r, "123", "nonce", nil)
	require.NoError(t, err)
	require.NotEqual(t, v2, other)
	r.Header.Set("Idempotency-Key", "request-2")
	changed, err := SigningRequest(r, "123", "nonce", nil)
	require.NoError(t, err)
	require.NotEqual(t, changed, other)
}

func TestSigningRequestRejectsAmbiguousIdentity(t *testing.T) {
	for _, tc := range []struct{ version, key string }{
		{"1", "request"}, {"2", ""}, {"3", "request"}, {"2", "with space"}, {"2", "with\nnewline"},
	} {
		t.Run(tc.version+tc.key, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/open/v1/balance", nil)
			r.Header.Set("X-Signature-Version", tc.version)
			r.Header.Set("Idempotency-Key", tc.key)
			_, err := SigningRequest(r, "123", "nonce", nil)
			require.Error(t, err)
		})
	}
	for _, header := range []string{"X-Signature-Version", "Idempotency-Key"} {
		r := httptest.NewRequest("GET", "/open/v1/balance", nil)
		r.Header.Set("X-Signature-Version", "2")
		r.Header.Set("Idempotency-Key", "request")
		r.Header.Add(header, "duplicate")
		_, err := SigningRequest(r, "123", "nonce", nil)
		require.Error(t, err)
	}
}

func TestCallProcessingReplaySkipsBusinessChecks(t *testing.T) {
	svc, _ := newCallTestService(t)
	call := svc.NewCall(Principal{TenantID: 1, ClientID: 1}, Route{ID: 1, Method: "GET"}, "", "", "")
	require.NoError(t, svc.Start(context.Background(), call))
	checked := false
	_, err := svc.RunRequest(context.Background(), &call, nil, func(context.Context, Call) error {
		checked = true
		return nil
	})
	require.Equal(t, 10004, errs.From(err).Code)
	require.False(t, checked)
}
