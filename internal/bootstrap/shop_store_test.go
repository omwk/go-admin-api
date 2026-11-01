package bootstrap

import (
	"context"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/scope"
	shopmodel "github.com/omwk/go-admin-api/internal/modules/shop/model"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

func dryShopStore(t *testing.T) (*shopStore, *sqlCapture) {
	t.Helper()
	capture := &sqlCapture{Interface: gormlog.Discard}
	base, err := gorm.Open(postgres.Open(""), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: capture})
	require.NoError(t, err)
	return &shopStore{raw: func(ctx context.Context) (*gorm.DB, context.CancelFunc) {
		return base.Session(&gorm.Session{NewDB: true}).WithContext(ctx), func() {}
	}}, capture
}

func TestShopDryRunMatrixIncludesScopeOnAllRootOperations(t *testing.T) {
	for _, kind := range []string{"all", "dept", "dept_and_child", "self", "custom"} {
		t.Run(kind, func(t *testing.T) {
			ctx := scope.WithAccess(context.Background(), scope.Access{Tenant: 17, User: 29, Department: 31, Path: "1.31", Roles: []scope.Role{{Kind: kind, Departments: []int64{31, 32}}}})
			store, capture := dryShopStore(t)
			_, _, err := store.Shops(ctx, 17, repo.Page{Number: 1, Size: 20, Sort: "-created_at"})
			require.NoError(t, err)
			_, err = store.Shop(ctx, 17, 29)
			require.NoError(t, err)
			_ = store.UpdateShop(ctx, 17, 29, 0, map[string]any{"name": "changed", "version": 1})
			_ = store.DeleteShop(ctx, 17, 29)
			sql := ""
			for _, statement := range capture.statements {
				sql += statement + "\n"
			}
			require.Contains(t, sql, "shop.tenant_id = 17")
			require.Contains(t, sql, "shop.deleted_at IS NULL")
			switch kind {
			case "self":
				require.Contains(t, sql, "shop.owner_user_id = 29")
			case "dept":
				require.Contains(t, sql, "shop.dept_id = 31")
			case "custom":
				require.Contains(t, sql, "shop.dept_id IN (31,32)")
			case "dept_and_child":
				require.Contains(t, sql, "scope_dept.path = '1.31' OR scope_dept.path LIKE '1.31' || '.%'")
			}
		})
	}
}

func TestShopStorePlatformIsGlobalAndCreateIsScoped(t *testing.T) {
	store, capture := dryShopStore(t)
	_, err := store.Platforms(context.Background())
	require.NoError(t, err)
	ctx := scope.WithAccess(context.Background(), scope.Access{Tenant: 17, User: 29, Department: 31, Roles: []scope.Role{{Kind: "all"}}})
	row := &shopmodel.Shop{TenantID: 17, PlatformID: 1, Name: "shop", ExternalShopID: "ext", Timezone: "UTC", Currency: "CNY", OwnerUserID: 29, DepartmentID: 31, Status: "active"}
	_ = store.CreateShop(ctx, row)
	joined := ""
	for _, statement := range capture.statements {
		joined += statement + "\n"
	}
	require.Contains(t, joined, "INSERT INTO \"shop\"")
	require.NotContains(t, joined, "SELECT *")
}
