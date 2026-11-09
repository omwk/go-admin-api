package bootstrap

import (
	"bytes"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/modules/gateway"
	"github.com/stretchr/testify/require"
)

func TestGatewaySecretIntegrationRotationRevocationAndExpiry(t *testing.T) {
	// REQ-GW-002/003/009: both keys work; expiry and revocation use current primary state.
	f := newGatewayFixture(t)
	first := f.signed(t, f.issued.Secret)
	_, err := f.auth.Authenticate(f.ctx, first)
	require.NoError(t, err)
	require.Equal(t, gateway.NonceTTL, f.server.TTL("gateway:app:"+f.client.AppID+":nonce:"+first.Header.Get("X-Nonce")))
	_, err = f.auth.Authenticate(f.ctx, first)
	require.Equal(t, 80103, errs.From(err).Code)
	second, err := f.service.Rotate(f.ctx, f.actor(), f.client.ID, time.Hour)
	require.NoError(t, err)
	for _, text := range []string{f.issued.Secret, second.Secret} {
		_, err := f.auth.Authenticate(f.ctx, f.signed(t, text))
		require.NoError(t, err)
	}
	f.now = f.now.Add(time.Hour)
	_, err = f.auth.Authenticate(f.ctx, f.signed(t, f.issued.Secret))
	require.Equal(t, 80102, errs.From(err).Code)
	_, err = f.auth.Authenticate(f.ctx, f.signed(t, second.Secret))
	require.NoError(t, err)
	require.NoError(t, f.service.Revoke(f.ctx, f.actor(), f.client.ID, second.ID))
	_, err = f.auth.Authenticate(f.ctx, f.signed(t, second.Secret))
	require.Equal(t, 80102, errs.From(err).Code)
	require.Equal(t, "应用凭证不可用", errs.From(err).Message)
}

func TestGatewaySecretIntegrationConcurrentIssuance(t *testing.T) {
	f := newGatewayFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.service.Rotate(f.ctx, f.actor(), f.client.ID, time.Hour)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, 409, errs.From(err).HTTPStatus)
		}
	}
	require.Equal(t, 1, successes)
	keys, err := f.store.Secrets(f.ctx, f.client.TenantID, f.client.ID)
	require.NoError(t, err)
	require.Len(t, keys, 2)
}

func TestGatewaySecretIntegrationCipherAndSuspension(t *testing.T) {
	f := newGatewayFixture(t)
	keys, err := f.store.Secrets(f.ctx, f.client.TenantID, f.client.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	decoded, err := base64.StdEncoding.DecodeString(f.issued.Secret)
	require.NoError(t, err)
	defer clear(decoded)
	require.Len(t, decoded, 32)
	require.Len(t, keys[0].Fingerprint, 12)
	require.Len(t, keys[0].Nonce, 12)
	require.False(t, bytes.Contains(keys[0].CipherText, []byte(f.issued.Secret)), "ciphertext contains issued key")
	require.NoError(t, f.service.SetStatus(f.ctx, f.actor(), f.client.ID, f.client.Version, "suspended"))
	_, err = f.auth.Authenticate(f.ctx, f.signed(t, f.issued.Secret))
	require.Equal(t, 80101, errs.From(err).Code)
	require.Equal(t, "应用凭证不可用", errs.From(err).Message)
	require.Equal(t, 10409, errs.From(f.service.SetStatus(f.ctx, f.actor(), f.client.ID, f.client.Version, "active")).Code)
}

func TestGatewaySecretIntegrationRedisFailureAndAuditRollback(t *testing.T) {
	f := newGatewayFixture(t)
	f.server.SetError("injected outage")
	_, err := f.auth.Authenticate(f.ctx, f.signed(t, f.issued.Secret))
	require.Equal(t, 503, errs.From(err).HTTPStatus)
	f.server.SetError("")
	broken, err := gateway.NewService(&gatewayAuditFailure{gatewayStore: f.store}, f.sealer, func() time.Time { return f.now })
	require.NoError(t, err)
	_, err = broken.Rotate(f.ctx, f.actor(), f.client.ID, time.Hour)
	require.Error(t, err)
	keys, err := f.store.Secrets(f.ctx, f.client.TenantID, f.client.ID)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Nil(t, keys[0].ExpiresAt)
}
