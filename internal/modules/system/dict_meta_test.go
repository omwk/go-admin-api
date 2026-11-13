package system

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/stretchr/testify/require"
)

type metaStore struct {
	DictStore
	reads int
}

func (s *metaStore) DictionaryByType(_ context.Context, tenant int64, kind string) (Dict, error) {
	s.reads++
	if kind == "missing" {
		return Dict{}, notFound()
	}
	return Dict{ID: tenant, Status: "active", Scope: DictScopeBusiness}, nil
}

func (s *metaStore) DictionaryItems(_ context.Context, tenant, _ int64, _, _ int, active bool) (Page[DictItem], error) {
	if !active {
		return Page[DictItem]{}, invalidDict()
	}
	return Page[DictItem]{List: []DictItem{{Label: "Enabled", Value: "enabled", Sort: int(tenant), Extra: json.RawMessage(`{"icon":"check"}`)}}}, nil
}

func TestDictionaryMetaCacheExpiresAtSixtySeconds(t *testing.T) {
	// S2 §3.3: deterministic expiry, independent tenants, safe ownership, explicit invalidation.
	store := &metaStore{}
	service, err := NewDictService(store, func(context.Context, audit.Event) error { return nil })
	require.NoError(t, err)
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	first, err := service.Meta(ctx, Actor{TenantID: 1}, "order_status")
	require.NoError(t, err)
	require.Equal(t, DictScopeBusiness, first[0].Scope)
	first[0].Extra[0] = 'x'
	now = now.Add(59 * time.Second)
	again, err := service.Meta(ctx, Actor{TenantID: 1}, "order_status")
	require.NoError(t, err)
	require.JSONEq(t, `{"icon":"check"}`, string(again[0].Extra))
	require.Equal(t, DictScopeBusiness, again[0].Scope)
	require.Equal(t, 1, store.reads)
	other, err := service.Meta(ctx, Actor{TenantID: 2}, "order_status")
	require.NoError(t, err)
	require.Equal(t, 2, other[0].Sort)
	require.Equal(t, 2, store.reads)
	now = now.Add(time.Second)
	_, err = service.Meta(ctx, Actor{TenantID: 1}, "order_status")
	require.NoError(t, err)
	require.Equal(t, 3, store.reads)
	service.Invalidate(1)
	_, err = service.Meta(ctx, Actor{TenantID: 1}, "order_status")
	require.NoError(t, err)
	require.Equal(t, 4, store.reads)
	for range 2 {
		_, err = service.Meta(ctx, Actor{TenantID: 1}, "missing")
		require.Equal(t, errs.ErrNotFound, errs.From(err).Code)
	}
	require.Equal(t, 6, store.reads)
}
