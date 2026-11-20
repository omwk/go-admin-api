package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEndpoint(t *testing.T) {
	for _, test := range []struct {
		input  string
		port   int
		secure bool
		want   string
		ssl    bool
	}{
		{"storage.example", 443, true, "storage.example:443", true},
		{"storage.example", 9000, false, "storage.example:9000", false},
		{"https://storage.example", 9443, false, "storage.example:9443", true},
		{"http://storage.example:9001", 443, true, "storage.example:9001", false},
		{"https://[::1]:9443/", 443, false, "[::1]:9443", true},
	} {
		t.Run(test.input, func(t *testing.T) {
			host, secure, err := NormalizeEndpoint(test.input, test.port, test.secure)
			require.NoError(t, err)
			require.Equal(t, test.want, host)
			require.Equal(t, test.ssl, secure)
		})
	}
	for _, input := range []string{"", "ftp://storage.example", "https://user:pass@storage.example", "https://storage.example/path", "https://storage.example?a=b", "host:65536", "host#fragment"} {
		_, _, err := NormalizeEndpoint(input, 443, true)
		require.Error(t, err)
	}
}

type providerMemory struct {
	current Provider
	rows    map[int64]Provider
}

func (s *providerMemory) Provider(_ context.Context, id int64) (Provider, error) {
	return s.rows[id], nil
}
func (s *providerMemory) DefaultProvider(context.Context) (Provider, error) { return s.current, nil }

func TestResolverLiveDefaultAndPinnedIdentity(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	p := Provider{ID: 1, TenantID: 1, Endpoint: "localhost", Port: 9000, Bucket: "first", Region: "us-east-1", AccessKey: hex.EncodeToString(key[:8]), SecretKey: hex.EncodeToString(key)}
	p.SecretEnc, err = SealSecret(sealer, p)
	require.NoError(t, err)
	source := &providerMemory{current: p, rows: map[int64]Provider{1: p}}
	resolver, err := NewResolver(source, sealer)
	require.NoError(t, err)
	selected, err := resolver.Default(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, selected.ProviderID)
	p.ID, p.Bucket = 2, "second"
	p.SecretEnc, err = SealSecret(sealer, p)
	require.NoError(t, err)
	source.current = p
	selected, err = resolver.Default(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, selected.ProviderID)
	id := int64(1)
	selected, err = resolver.Resolve(context.Background(), &id)
	require.NoError(t, err)
	require.Equal(t, "first", selected.Bucket)
	p.ID = 3
	_, err = OpenSecret(sealer, p)
	require.Error(t, err, "AAD must reject swapped provider ciphertext")
	source.current = Provider{}
	_, err = resolver.Default(context.Background())
	require.Equal(t, 409, errs.From(err).HTTPStatus)
}
