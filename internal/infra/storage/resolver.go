package storage

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// ProviderSource reads current encrypted configuration on each operation.
type ProviderSource interface {
	Provider(context.Context, int64) (Provider, error)
	DefaultProvider(context.Context) (Provider, error)
}

// Resolver selects the platform default or a historical provider without caching.
type Resolver struct {
	source ProviderSource
	sealer crypto.Sealer
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
	return &Resolver{source: source, sealer: sealer}, nil
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
	p.SecretKey, err = OpenSecret(r.sealer, p)
	if err != nil {
		return Resolved{}, err
	}
	object, err := NewProviderS3(p)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{ProviderID: p.ID, Bucket: p.Bucket, Object: object}, nil
}

// Unconfigured is the stable public error for missing object storage.
func Unconfigured() error { return errs.New(errs.ErrConflict, 409, "对象存储未配置") }
