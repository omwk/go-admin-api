package identity

import (
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/stretchr/testify/require"
)

// S1b only adds department menus. These existing API capabilities have no
// corresponding page/button in the minimum seed catalog. Remove an exemption
// when its menu projection is introduced; never exempt a whole module/prefix.
var menuProjectionExempt = map[string]string{
	permission.SystemSettingList:        "S2 settings are API-only; the settings page is a separate task.",
	permission.SystemSettingUpdate:      "S2 settings are API-only; the settings page is a separate task.",
	permission.SystemDictList:           "S2 dictionaries are API-only; dictionary pages are a separate task.",
	permission.SystemDictCreate:         "S2 dictionaries are API-only; dictionary pages are a separate task.",
	permission.SystemDictUpdate:         "S2 dictionaries are API-only; dictionary pages are a separate task.",
	permission.SystemDictDelete:         "S2 dictionaries are API-only; dictionary pages are a separate task.",
	permission.SystemSettingWrite:       "Notification channel configuration is API-only in the minimum seed.",
	permission.SystemUserCreate:         "The user page has no seeded create button yet.",
	permission.SystemUserUpdate:         "The user page has no seeded edit button yet.",
	permission.SystemUserDelete:         "The user page has no seeded delete button yet.",
	permission.SystemUserReset:          "The user page has no seeded password reset button yet.",
	permission.SystemUserGrant:          "The user page has no seeded role assignment button yet.",
	permission.SystemRoleCreate:         "The role page has no seeded create button yet.",
	permission.SystemRoleUpdate:         "The role page has no seeded edit button yet.",
	permission.SystemRoleDelete:         "The role page has no seeded delete button yet.",
	permission.SystemRoleGrant:          "The role page has no seeded grant button yet.",
	permission.SystemMenuCreate:         "The menu page has no seeded create button yet.",
	permission.SystemMenuUpdate:         "The menu page has no seeded edit button yet.",
	permission.SystemMenuDelete:         "The menu page has no seeded delete button yet.",
	permission.SystemSessionList:        "Session management has no page in the minimum seed.",
	permission.SystemSessionKick:        "Session management has no kick button in the minimum seed.",
	permission.TradeOrderRead:           "Order details have no dedicated seeded route.",
	permission.TradeOrderStats:          "Order statistics have no page in the minimum seed.",
	permission.TradeOrderPIIRead:        "Order PII disclosure has no seeded action button.",
	permission.TradeOrderExport:         "Order exports have no seeded action button.",
	permission.TradeCustomerList:        "The seed uses the CRM customer entry rather than the trade customer page.",
	permission.TradeCustomerRead:        "Trade customer details have no seeded route.",
	permission.TradeCustomerCreate:      "Trade customer creation has no seeded button.",
	permission.TradeCustomerUpdate:      "Trade customer editing has no seeded button.",
	permission.TradeCustomerDelete:      "Trade customer deletion has no seeded button.",
	permission.TradePriceListList:       "Price lists have no page in the minimum seed.",
	permission.TradePriceListRead:       "Price list details have no seeded route.",
	permission.TradePriceListCreate:     "Price list creation has no seeded button.",
	permission.TradePriceListUpdate:     "Price list editing has no seeded button.",
	permission.TradePriceListDelete:     "Price list deletion has no seeded button.",
	permission.TradePriceListWrite:      "Price list item maintenance has no seeded button.",
	permission.TradePriceListPreview:    "Customer price preview has no seeded button.",
	permission.ShareLinkList:            "Share links have no page in the minimum seed.",
	permission.ShareLinkCreate:          "Share publishing has no seeded button.",
	permission.ShareLinkUpdate:          "Share revocation and republishing have no seeded button.",
	permission.CRMInquiryUpdate:         "The inquiry page has no seeded processing button.",
	permission.TradeInquiryPIIRead:      "Inquiry PII disclosure has no seeded action button.",
	permission.TradeInquiryExport:       "Inquiry exports have no seeded action button.",
	permission.ShareVisitList:           "Share visit details have no page in the minimum seed.",
	permission.ShopStoreList:            "Shops have no page in the minimum seed.",
	permission.ShopStoreCreate:          "Shop creation has no seeded button.",
	permission.ShopStoreRead:            "Shop details have no seeded route.",
	permission.ShopStoreUpdate:          "Shop editing has no seeded button.",
	permission.ShopStoreDelete:          "Shop deletion has no seeded button.",
	permission.ShopCredentialList:       "Credentials have no page in the minimum seed.",
	permission.ShopCredentialCreate:     "Credential entry has no seeded button.",
	permission.ShopCredentialRotate:     "Credential rotation has no seeded button.",
	permission.ShopCredentialCheck:      "Credential checks have no seeded button.",
	permission.ShopCredentialDisable:    "Credential disabling has no seeded button.",
	permission.ShopCredentialAudit:      "Credential audits have no page in the minimum seed.",
	permission.SyncJobRun:               "Sync execution has no seeded button.",
	permission.SyncRunList:              "Sync runs have no page in the minimum seed.",
	permission.SyncRunRead:              "Sync run details have no seeded route.",
	permission.SyncDLQList:              "The sync dead letter queue has no seeded page.",
	permission.SyncDLQReplay:            "Dead letter replay has no seeded button.",
	permission.SyncQuarantineList:       "Quarantined sync records have no seeded page.",
	permission.SyncQuarantineReplay:     "Quarantine replay has no seeded button.",
	permission.SyncReplayRun:            "Range replay has no seeded button.",
	permission.SyncReplayCreate:         "Historical replay creation has no seeded button.",
	permission.SyncReplayList:           "Historical replay has no page in the minimum seed.",
	permission.SyncReplayRead:           "Historical replay details have no seeded route.",
	permission.SyncReplayCancel:         "Historical replay cancellation has no seeded button.",
	permission.SyncDriftList:            "Schema drift has no page in the minimum seed.",
	permission.SyncDriftAck:             "Drift acknowledgement has no seeded button.",
	permission.SyncDriftResolve:         "Drift resolution has no seeded button.",
	permission.SyncDriftIgnore:          "Drift suppression has no seeded button.",
	permission.SyncBaselinePromote:      "Baseline promotion has no seeded button.",
	permission.ProductSPUList:           "Products have no page in the minimum seed.",
	permission.ProductSPURead:           "Product details have no seeded route.",
	permission.ProductSPUCreate:         "Product creation has no seeded button.",
	permission.ProductSPUUpdate:         "Product editing has no seeded button.",
	permission.ProductSPUDelete:         "Product deletion has no seeded button.",
	permission.ProductSPUMerge:          "Product merging has no seeded button.",
	permission.ProductSPUExport:         "Product exports have no seeded button.",
	permission.ProductCategoryList:      "Product categories have no page in the minimum seed.",
	permission.ProductCategoryWrite:     "Category maintenance has no seeded button.",
	permission.ProductCategoryMap:       "Category mapping has no seeded button.",
	permission.GatewayClientList:        "Open applications have no page in the minimum seed.",
	permission.GatewayClientCreate:      "Open application creation has no seeded button.",
	permission.GatewayClientUpdate:      "Open application status changes have no seeded button.",
	permission.GatewayClientRotate:      "Open application key rotation has no seeded button.",
	permission.GatewayClientGrant:       "Open application grants have no seeded button.",
	permission.GatewayRouteList:         "Open routes have no page in the minimum seed.",
	permission.GatewayRouteCreate:       "Open capability registration has no seeded button.",
	permission.GatewayRouteUpdate:       "Open route status changes have no seeded button.",
	permission.BillingWalletList:        "Wallets and transactions have no page in the minimum seed.",
	permission.BillingWalletRecharge:    "Wallet recharges have no seeded button.",
	permission.BillingWalletAdjust:      "Wallet adjustments have no seeded button.",
	permission.BillingPlanList:          "Plans have no page in the minimum seed.",
	permission.BillingPlanCreate:        "Plan creation has no seeded button.",
	permission.BillingPlanUpdate:        "Plan editing has no seeded button.",
	permission.BillingPlanGrant:         "Plan purchases and grants have no seeded button.",
	permission.BillingReconcileList:     "Reconciliation has no page in the minimum seed.",
	permission.BillingReconcileRun:      "Manual reconciliation has no seeded button.",
	permission.BillingReconcileResolve:  "Reconciliation compensation has no seeded button.",
	permission.BillingStatementList:     "Statements have no page in the minimum seed.",
	permission.BillingStatementGenerate: "Statement drafting has no seeded button.",
	permission.BillingStatementIssue:    "Statement issuance has no seeded button.",
}

func TestPermissionRegistryIsProjectedToMenus(t *testing.T) {
	// S1b: every newly registered code requires a menu projection or a reasoned exception.
	registered, projected := map[string]bool{}, map[string]bool{}
	for _, item := range permission.All() {
		registered[item.Code] = true
	}
	for _, menu := range seedMenus() {
		if menu.Row.PermissionCode == nil || *menu.Row.PermissionCode == "" {
			continue
		}
		code := *menu.Row.PermissionCode
		require.True(t, registered[code], "seed menu projects unregistered permission %s", code)
		projected[code] = true
	}
	for _, item := range permission.All() {
		t.Run(item.Code, func(t *testing.T) {
			_, exempt := menuProjectionExempt[item.Code]
			require.True(t, projected[item.Code] || exempt, "permission %s has no seed menu projection or explicit exemption", item.Code)
		})
	}
	for code, reason := range menuProjectionExempt {
		require.True(t, registered[code], "exemption references unregistered permission %s", code)
		require.NotEmpty(t, strings.TrimSpace(reason), "exemption %s needs a reason", code)
		require.False(t, projected[code], "remove stale exemption for projected permission %s", code)
	}
	t.Logf("registered=%d projected=%d exempt=%d", len(registered), len(projected), len(menuProjectionExempt))
}
