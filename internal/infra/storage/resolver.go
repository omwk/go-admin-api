package storage

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// ProviderSource reads current encrypted configuration on each operation.
type ProviderSource interface {
	Provider(context.Context, int64) (Provider, error)
	DefaultProvider(context.Context) (Provider, error)
}

// Resolver reads live providers and reuses clients while their configuration matches.
type Resolver struct {
	source  ProviderSource
	sealer  crypto.Sealer
	mu      sync.Mutex
	clients map[int64]cachedClient
}

type cachedClient struct {
	provider Provider
	object   *S3
}

// Resolved pins every operation in an upload to one configuration.
type Resolved struct {
	ProviderID int64
	Bucket     string
	Object     *S3
}

// NewResolver builds a live database-backed storage resolver.
func NewResolver(source ProviderSource, sealer crypto.Sealer) (*Resolver, error) {
	if source == nil {
		return nil, errors.New("storage source required")
	}
	return &Resolver{source: source, sealer: sealer, clients: make(map[int64]cachedClient)}, nil
}

// Default resolves the current enabled platform default.
func (r *Resolver) Default(ctx context.Context) (Resolved, error) { return r.Resolve(ctx, nil) }

// Resolve reads the persisted provider ID, falling back only for legacy null IDs.
func (r *Resolver) Resolve(ctx context.Context, id *int64) (Resolved, error) {
	var p Provider
	var err error
	if id == nil {
		p, err = r.source.DefaultProvider(ctx)
	} else {
		p, err = r.source.Provider(ctx, *id)
	}
	if err != nil {
		return Resolved{}, err
	}
	if p.ID <= 0 {
		return Resolved{}, Unconfigured()
	}
	object, err := r.client(p)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{ProviderID: p.ID, Bucket: p.Bucket, Object: object}, nil
}

func (r *Resolver) client(p Provider) (*S3, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.clients[p.ID]; ok && sameClientConfig(cached.provider, p) {
		return cached.object, nil
	}
	configuration := p
	var err error
	p.SecretKey, err = OpenSecret(r.sealer, p)
	if err != nil {
		return nil, err
	}
	object, err := NewProviderS3(p)
	if err != nil {
		return nil, err
	}
	configuration.SecretKey = ""
	configuration.SecretEnc = bytes.Clone(configuration.SecretEnc)
	r.clients[p.ID] = cachedClient{provider: configuration, object: object}
	return object, nil
}

func sameClientConfig(a, b Provider) bool {
	return a.TenantID == b.TenantID && a.Endpoint == b.Endpoint && a.Port == b.Port &&
		a.Bucket == b.Bucket && a.UseSSL == b.UseSSL && a.PathStyle == b.PathStyle &&
		a.Region == b.Region && a.AccessKey == b.AccessKey && bytes.Equal(a.SecretEnc, b.SecretEnc)
}

// Unconfigured is the stable public error for missing object storage.
func Unconfigured() error { return errs.New(errs.ErrConflict, 409, "对象存储未配置") }
