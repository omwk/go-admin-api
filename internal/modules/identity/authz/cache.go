// Package authz owns versioned authorization payloads and bounded post-commit invalidation.
package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/cache"
)

// Backend exposes the version and payload operations, including per-key pipeline outcomes.
type Backend interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string, time.Duration) error
	Version(context.Context, string) (string, bool, error)
	IncrBatch(context.Context, []string) []cache.IncrementResult
}

// Cache uses one generation for permissions and menus, and never caches session validity.
type Cache struct{ backend Backend }

// New requires the configured Redis dependency.
func New(backend Backend) *Cache { return &Cache{backend: backend} }

// VersionKey is the shared, non-expiring authorization generation key.
func VersionKey(tenant, user int64) string { return fmt.Sprintf("authz:ver:%d:%d", tenant, user) }

type payload struct {
	Version string          `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// Load reads a version before a payload and fills only the exact generation it read.
func (c *Cache) Load(ctx context.Context, tenant, user int64, kind string, loader func(context.Context) ([]byte, error)) ([]byte, error) {
	if tenant <= 0 || user <= 0 || (kind != "perm" && kind != "menu") {
		return nil, errors.New("invalid authorization cache identity")
	}
	version, fresh, err := c.backend.Version(ctx, VersionKey(tenant, user))
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("authz:%s:%d:%d:v%s", kind, tenant, user, version)
	if data, hit, err := c.readPayload(ctx, key, version, fresh); err != nil || hit {
		return data, err
	}
	data, err := loader(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload{Version: version, Data: data})
	if err != nil {
		return nil, err
	}
	if err := c.backend.Set(ctx, key, string(encoded), 5*time.Minute); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *Cache) readPayload(ctx context.Context, key, version string, fresh bool) ([]byte, bool, error) {
	if fresh {
		return nil, false, nil
	}
	raw, found, err := c.backend.Get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	var item payload
	if found && json.Unmarshal([]byte(raw), &item) == nil && item.Version == version && json.Valid(item.Data) {
		return item.Data, true, nil
	}
	return nil, false, nil
}
