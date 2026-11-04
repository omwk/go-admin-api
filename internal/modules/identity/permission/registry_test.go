package permission

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegistryAndPermissionValidation(t *testing.T) {
	for _, item := range All() {
		require.NoError(t, Validate(item.Module, item.Code))
	}
	for _, code := range []string{"system:user", "system:user:list:extra", "system::list", "System:user:list", "system:user:*", "trade:user:list"} {
		require.Error(t, Validate("system", code))
	}
}

func TestM4CatalogPermissionsAreRegistered(t *testing.T) {
	for _, code := range []string{
		ProductSPUList, ProductSPURead, ProductSPUCreate, ProductSPUUpdate, ProductSPUDelete, ProductSPUMerge, ProductSPUExport,
		ProductCategoryList, ProductCategoryWrite, ProductCategoryMap,
	} {
		require.NoError(t, Registered(code), code)
	}
}
