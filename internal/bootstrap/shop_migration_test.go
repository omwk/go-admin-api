package bootstrap

import (
	"encoding/json"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/stretchr/testify/require"
)

func TestShopMigrationIntegrationSeededPlatforms(t *testing.T) {
	// REQ-SHP-001: a fresh schema contains exactly the three approved global platforms.
	database, ctx := isolatedIdentityDB(t)
	for i := 0; i < 2; i++ {
		_, err := database.Migrate(ctx, "up")
		require.NoError(t, err)
		q, cancel := database.WithContext(ctx)
		var platforms []model.Platform
		err = q.Select("id, code, name, auth_type, capabilities, config_schema, status").Order("code").Find(&platforms).Error
		cancel()
		require.NoError(t, err)
		require.Len(t, platforms, 3)
		for index, code := range []string{"shopee", "shopify", "taobao"} {
			p := platforms[index]
			require.Equal(t, code, p.Code)
			require.Equal(t, "active", p.Status)
			require.JSONEq(t, `{}`, string(p.ConfigSchema))
			var capabilities struct {
				Resources    []string `json:"resources"`
				TokenRefresh bool     `json:"token_refresh"`
			}
			require.NoError(t, json.Unmarshal(p.Capabilities, &capabilities))
			require.Equal(t, []string{"product", "order", "inventory"}, capabilities.Resources)
			require.Equal(t, code == "shopee", capabilities.TokenRefresh)
			require.Equal(t, map[string]string{"shopee": "oauth2", "shopify": "api_key", "taobao": "session"}[code], p.AuthType)
		}
	}
}

func TestMergedScopeRegistryPreservesIdentityAndShopSkeleton(t *testing.T) {
	r, err := scopeRegistry()
	require.NoError(t, err)
	for port, count := range map[string]int{"Admin": 36, "Sessions": 2, "Auth": 11, "Profile": 3, "Seed": 6, "Shop": 7} {
		require.Len(t, r.QueryNames(port), count)
	}
	shop, err := r.Lookup("shop")
	require.NoError(t, err)
	require.Equal(t, "owner_user_id", shop.OwnerColumn)
	require.Equal(t, "dept_id", shop.DepartmentColumn)
	for entity, parent := range map[string]string{"shop_credential": "shop", "credential_version": "shop_credential", "credential_audit": "shop_credential"} {
		d, err := r.Lookup(entity)
		require.NoError(t, err)
		require.NotNil(t, d.Parent)
		require.Equal(t, parent, d.Parent.Entity)
		require.Empty(t, d.OwnerColumn)
		require.Empty(t, d.DepartmentColumn)
	}
	platform, err := r.Lookup("platform")
	require.NoError(t, err)
	require.True(t, platform.Global)
	require.Empty(t, platform.TenantColumn)
	for _, name := range []string{"Shop.Platforms", "Shop.Shops", "Shop.Shop", "Shop.ValidateAssignment", "Shop.CreateShop", "Shop.UpdateShop", "Shop.DeleteShop"} {
		_, err = r.Query(name)
		require.NoError(t, err)
	}
}
