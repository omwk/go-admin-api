package storage

import (
	"context"
	"crypto/rand"
	"sync"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/stretchr/testify/require"
)

func TestResolverReusesClientAndRefreshesConfiguration(t *testing.T) {
	// BE-IDN-022: default and historical operations share one cached SDK client.
	resolver, source, sealer := lookupResolver(t)
	ctx := context.Background()
	first, err := resolver.Default(ctx)
	require.NoError(t, err)
	again, err := resolver.Resolve(ctx, &first.ProviderID)
	require.NoError(t, err)
	require.Same(t, first.Object, again.Object)

	p := source.current
	p.Name = "renamed"
	source.current = p
	again, err = resolver.Default(ctx)
	require.NoError(t, err)
	require.Same(t, first.Object, again.Object, "non-client metadata must not evict the client")

	p.Bucket = "updated-bucket"
	source.current = p
	updated, err := resolver.Default(ctx)
	require.NoError(t, err)
	require.NotSame(t, first.Object, updated.Object)
	require.Equal(t, "lookup-test", first.Bucket, "in-flight binding remains pinned")
	require.Equal(t, p.Bucket, updated.Bucket)

	p.SecretKey = lookupProvider(t).SecretKey
	p.SecretEnc, err = SealSecret(sealer, p)
	require.NoError(t, err)
	source.current = p
	rotated, err := resolver.Default(ctx)
	require.NoError(t, err)
	require.NotSame(t, updated.Object, rotated.Object, "credential rotation must invalidate the cache")

	source.current = Provider{}
	_, err = resolver.Default(ctx)
	require.Error(t, err, "a removed default must not be served from cache")
}

func TestResolverRefreshesClientOptions(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Provider)
	}{
		{"endpoint", func(p *Provider) { p.Endpoint = "s3.eu-west-1.amazonaws.com" }},
		{"port", func(p *Provider) { p.Port = 9443 }},
		{"tls", func(p *Provider) { p.UseSSL = !p.UseSSL }},
		{"region", func(p *Provider) { p.Region = "eu-west-1" }},
		{"lookup", func(p *Provider) { p.PathStyle = !p.PathStyle }},
		{"access", func(p *Provider) { p.AccessKey = lookupProvider(t).AccessKey }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, source, _ := lookupResolver(t)
			first, err := r.Default(context.Background())
			require.NoError(t, err)
			test.edit(&source.current)
			second, err := r.Default(context.Background())
			require.NoError(t, err)
			require.NotSame(t, first.Object, second.Object)
		})
	}
}

func TestResolverConcurrentClientReuse(t *testing.T) {
	r, _, _ := lookupResolver(t)
	var workers sync.WaitGroup
	objects := make(chan Resolved, 16)
	errorsFound := make(chan error, 16)
	for i := 0; i < cap(objects); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			object, err := r.Default(context.Background())
			objects <- object
			errorsFound <- err
		}()
	}
	workers.Wait()
	close(objects)
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
	first := <-objects
	for selected := range objects {
		require.Same(t, first.Object, selected.Object)
	}
}

func lookupResolver(t *testing.T) (*Resolver, *providerMemory, crypto.Sealer) {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	sealer, err := crypto.NewKeyring(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	p := lookupProvider(t)
	p.SecretEnc, err = SealSecret(sealer, p)
	require.NoError(t, err)
	source := &providerMemory{current: p, rows: map[int64]Provider{p.ID: p}}
	resolver, err := NewResolver(source, sealer)
	require.NoError(t, err)
	return resolver, source, sealer
}
