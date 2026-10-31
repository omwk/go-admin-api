package bootstrap

import (
	"context"
	"reflect"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
)

func TestShopRepositoryReflectionMatchesScopeDeclarations(t *testing.T) {
	contract := reflect.TypeOf((*repo.Store)(nil)).Elem()
	registered := scopeRegistryQueryNames("Shop")
	methods := make(map[string]bool)
	for i := 0; i < contract.NumMethod(); i++ {
		methods[contract.Method(i).Name] = true
	}
	for _, method := range registered {
		require.Contains(t, methods, method)
	}
	require.Len(t, registered, contract.NumMethod())
	for method := range methods {
		require.Contains(t, registered, method)
	}
}

func scopeRegistryQueryNames(port string) []string {
	registry, err := scopeRegistry()
	if err != nil {
		return nil
	}
	return registry.QueryNames(port)
}

func TestShopScopeRequiresVerifiedIdentity(t *testing.T) {
	store := &shopStore{}
	require.Error(t, store.ValidateAssignment(context.Background(), 1, 1, 1))
}
