package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/refresh"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"github.com/stretchr/testify/require"
)

// BE-IDN-012: this page contract is independent of the seed implementation.
var declaredMenuPages = []struct {
	path, parent, code, component string
	hidden                        bool
}{
	{"/system/users", "/system", permission.SystemUserList, "system/user/index", false},
	{"/system/menus", "/system", permission.SystemMenuList, "system/menu/index", false},
	{"/system/roles", "/system", permission.SystemRoleList, "system/role/index", false},
	{"/system/depts", "/system", permission.SystemUserList, "system/dept/index", false},
	{"/system/notifications", "/system", permission.SystemNotifyList, "system/notification/index", false},
	{"/system/notifications/channels", "/system", permission.SystemNotifyManage, "system/notification/channels", false},
	{"/system/audit-logs", "/system", permission.SystemAuditList, "system/audit-log/index", false},
	{"/system/login-logs", "/system", permission.SystemAuditList, "system/login-log/index", false},
	{"/system/tenants", "/system", permission.SystemTenantList, "system/tenant/index", false},
	{"/system/tenants/templates", "/system/tenants", permission.SystemTenantManage, "system/tenant/index", true},
	{"/system/tenants/templates/:id", "/system/tenants", permission.SystemTenantManage, "system/tenant/template-editor", true},
	{"/system/dicts", "/system", permission.SystemDictList, "system/dict/index", false},
	{"/system/settings", "/system", permission.SystemSettingList, "system/setting/index", false},
	{"/system/storage", "/system", permission.SystemStorageManage, "system/storage/index", false},
	{"/system/files", "/system", permission.SystemFileManage, "system/files/index", false},
	{"/catalog/products", "/catalog", permission.ProductSPUList, "catalog/product/index", false},
	{"/catalog/products/:id", "/catalog/products", permission.ProductSPURead, "catalog/product/detail", true},
	{"/trade/orders", "/trade", permission.TradeOrderList, "trade/order/index", false},
	{"/crm/customers", "/crm", permission.CRMCustomerList, "crm/customer/index", false},
	{"/crm/inquiries", "/crm", permission.CRMInquiryList, "crm/inquiry/index", false},
	{"/open-platform/clients", "/open-platform", permission.GatewayClientList, "open-platform/clients/index", false},
	{"/open-platform/routes", "/open-platform", permission.GatewayRouteList, "open-platform/routes/index", false},
	{"/open-platform/calls", "/open-platform", permission.GatewayCallRead, "open-platform/calls/index", false},
	{"/open-platform/usage", "/open-platform", permission.GatewayCallRead, "open-platform/usage/index", false},
}

type menuSeedRecorder struct {
	seedRecorder
	menus []repo.SeedMenu
}

func (s *menuSeedRecorder) SeedMenus(_ context.Context, _ int64, menus []repo.SeedMenu) error {
	s.menus = menus
	return nil
}

func TestMenuSeedCoversDeclaredPages(t *testing.T) {
	password, err := refresh.Generate()
	require.NoError(t, err)
	for _, tenant := range []int64{1, 23} {
		t.Run(fmt.Sprintf("tenant-%d", tenant), func(t *testing.T) {
			store := &menuSeedRecorder{}
			service, err := NewSeedService(store)
			require.NoError(t, err)
			require.NoError(t, service.Run(context.Background(), tenant, password))
			require.NoError(t, checkDeclaredMenuPages(store.menus))
			t.Logf("tenant=%d declared pages=%d menus=%d", tenant, len(declaredMenuPages), len(store.menus))
		})
	}
}

func checkDeclaredMenuPages(menus []repo.SeedMenu) error {
	if err := checkSeedMenuComponents(menus); err != nil {
		return err
	}
	byPath := make(map[string]repo.SeedMenu, len(menus))
	for _, menu := range menus {
		if menu.Row.Path == nil {
			return fmt.Errorf("menu %s has no path", menu.Row.Name)
		}
		if _, duplicate := byPath[*menu.Row.Path]; duplicate {
			return fmt.Errorf("duplicate menu %s", *menu.Row.Path)
		}
		if menu.ParentPath != "" {
			if _, found := byPath[menu.ParentPath]; !found {
				return fmt.Errorf("missing preceding parent %s", menu.ParentPath)
			}
		}
		byPath[*menu.Row.Path] = menu
	}
	for _, page := range declaredMenuPages {
		menu, found := byPath[page.path]
		if !found {
			return fmt.Errorf("missing declared page %s", page.path)
		}
		row := menu.Row
		if row.Type != "menu" || row.PermissionCode == nil || *row.PermissionCode != page.code ||
			row.Component == nil || *row.Component != page.component || menu.ParentPath != page.parent ||
			row.Visible == page.hidden || row.Status != "active" {
			return fmt.Errorf("declared page contract mismatch %s", page.path)
		}
	}
	return nil
}

var seedComponentPattern = regexp.MustCompile(`^[a-z0-9/-]+$`)

func checkSeedMenuComponents(menus []repo.SeedMenu) error {
	for _, menu := range menus {
		component := menu.Row.Component
		if component != nil && (strings.HasPrefix(*component, "/") || !seedComponentPattern.MatchString(*component)) {
			return fmt.Errorf("invalid seed component %q", *component)
		}
	}
	return nil
}

func TestMenuSeedComponentsUseShorthand(t *testing.T) {
	// BE-IDN-026 §4.1: cover every component, including hidden and future pages.
	require.NoError(t, checkSeedMenuComponents(seedMenus()))
}

func TestMenuSeedComponentGuardRejectsInvalid(t *testing.T) {
	// BE-IDN-026 §4.3: the guard also checks pages outside the declared catalog.
	for _, component := range []string{"/x/y.vue", "/x/y/index", "x/y/index.vue", "x/Y/index", "x/y_index", ""} {
		t.Run(component, func(t *testing.T) {
			menus := append(seedMenus(), seedMenu("extra", "menu", "/extra", "", component, "", 1))
			menus[len(menus)-1].Row.Component = &component
			require.ErrorContains(t, checkDeclaredMenuPages(menus), "invalid seed component")
		})
	}
}

func TestMenuSeedCoverageRejectsMissingPage(t *testing.T) {
	for _, path := range []string{"/system/dicts", "/system/storage", "/system/files",
		"/open-platform/clients", "/open-platform/routes", "/open-platform/calls", "/open-platform/usage"} {
		t.Run(path, func(t *testing.T) {
			menus := seedMenus()
			for i, menu := range menus {
				if *menu.Row.Path == path {
					menus = append(menus[:i:i], menus[i+1:]...)
					break
				}
			}
			require.ErrorContains(t, checkDeclaredMenuPages(menus), "missing declared page "+path)
		})
	}
}
