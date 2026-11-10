// Package permission owns the three-part codes used by identity seeds and authorization.
package permission

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Codes cover the current system module and the published M1a compatibility entries.
const (
	BillingWalletList      = "billing:wallet:list"
	BillingWalletRecharge  = "billing:wallet:recharge"
	GatewayClientList      = "gateway:client:list"
	GatewayClientCreate    = "gateway:client:create"
	GatewayClientUpdate    = "gateway:client:update"
	GatewayClientRotate    = "gateway:client:rotate"
	GatewayClientGrant     = "gateway:client:grant"
	GatewayRouteList       = "gateway:route:list"
	GatewayRouteCreate     = "gateway:route:create"
	GatewayRouteUpdate     = "gateway:route:update"
	SystemSettingWrite     = "system:setting:write"
	SystemUserList         = "system:user:list"
	SystemUserCreate       = "system:user:create"
	SystemUserUpdate       = "system:user:update"
	SystemUserDelete       = "system:user:delete"
	SystemUserReset        = "system:user:reset"
	SystemUserGrant        = "system:user:grant"
	SystemRoleList         = "system:role:list"
	SystemRoleCreate       = "system:role:create"
	SystemRoleUpdate       = "system:role:update"
	SystemRoleDelete       = "system:role:delete"
	SystemRoleGrant        = "system:role:grant"
	SystemMenuList         = "system:menu:list"
	SystemMenuCreate       = "system:menu:create"
	SystemMenuUpdate       = "system:menu:update"
	SystemMenuDelete       = "system:menu:delete"
	SystemSessionList      = "system:session:list"
	SystemSessionKick      = "system:session:kick"
	TradeOrderList         = "trade:order:list"
	TradeOrderRead         = "trade:order:read"
	TradeOrderStats        = "trade:order:stats"
	TradeOrderPIIRead      = "trade:order:pii:read"
	TradeOrderExport       = "trade:order:export"
	TradeCustomerList      = "trade:customer:list"
	TradeCustomerRead      = "trade:customer:read"
	TradeCustomerCreate    = "trade:customer:create"
	TradeCustomerUpdate    = "trade:customer:update"
	TradeCustomerDelete    = "trade:customer:delete"
	TradePriceListList     = "trade:pricelist:list"
	TradePriceListRead     = "trade:pricelist:read"
	TradePriceListCreate   = "trade:pricelist:create"
	TradePriceListUpdate   = "trade:pricelist:update"
	TradePriceListDelete   = "trade:pricelist:delete"
	TradePriceListWrite    = "trade:pricelist:write"
	TradePriceListPreview  = "trade:pricelist:preview"
	CRMCustomerList        = "crm:customer:list"
	ShareLinkList          = "share:link:list"
	ShareLinkCreate        = "share:link:create"
	ShareLinkUpdate        = "share:link:update"
	CRMInquiryList         = "crm:inquiry:list"
	CRMInquiryUpdate       = "crm:inquiry:update"
	TradeInquiryPIIRead    = "trade:inquiry:pii:read"
	TradeInquiryExport     = "trade:inquiry:export"
	ShareVisitList         = "share:visit:list"
	ShopStoreList          = "shop:store:list"
	ShopStoreCreate        = "shop:store:create"
	ShopStoreRead          = "shop:store:read"
	ShopStoreUpdate        = "shop:store:update"
	ShopStoreDelete        = "shop:store:delete"
	shopPermissionModule   = "shop"
	shopCredentialResource = "credential"
	ShopCredentialList     = shopPermissionModule + ":" + shopCredentialResource + ":list"
	ShopCredentialCreate   = shopPermissionModule + ":" + shopCredentialResource + ":create"
	ShopCredentialRotate   = shopPermissionModule + ":" + shopCredentialResource + ":rotate"
	ShopCredentialCheck    = shopPermissionModule + ":" + shopCredentialResource + ":check"
	ShopCredentialDisable  = shopPermissionModule + ":" + shopCredentialResource + ":disable"
	ShopCredentialAudit    = shopPermissionModule + ":" + shopCredentialResource + ":audit"
	SyncJobRun             = "sync:job:run"
	SyncRunList            = "sync:run:list"
	SyncRunRead            = "sync:run:read"
	SyncDLQList            = "sync:dlq:list"
	SyncDLQReplay          = "sync:dlq:replay"
	SyncQuarantineList     = "sync:quarantine:list"
	SyncQuarantineReplay   = "sync:quarantine:replay"
	SyncReplayRun          = "sync:replay:run"
	SyncReplayCreate       = "sync:replay:create"
	SyncReplayList         = "sync:replay:list"
	SyncReplayRead         = "sync:replay:read"
	SyncReplayCancel       = "sync:replay:cancel"
	SyncDriftList          = "sync:drift:list"
	SyncDriftAck           = "sync:drift:ack"
	SyncDriftResolve       = "sync:drift:resolve"
	SyncDriftIgnore        = "sync:drift:ignore"
	SyncBaselinePromote    = "sync:baseline:promote"
	ProductSPUList         = "product:spu:list"
	ProductSPURead         = "product:spu:read"
	ProductSPUCreate       = "product:spu:create"
	ProductSPUUpdate       = "product:spu:update"
	ProductSPUDelete       = "product:spu:delete"
	ProductSPUMerge        = "product:spu:merge"
	ProductSPUExport       = "product:spu:export"
	ProductCategoryList    = "product:category:list"
	ProductCategoryWrite   = "product:category:write"
	ProductCategoryMap     = "product:category:map"
)

var segment = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Definition is a permission catalog entry, independent from persistence identifiers.
type Definition struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Module string `json:"module"`
	Type   string `json:"type"`
}

// All returns an owned copy of the permission catalog.
func All() []Definition {
	items := []Definition{
		{SystemSettingWrite, "维护通知渠道", "system", "api"},
		{SystemUserList, "查看用户", "system", "api"},
		{SystemUserCreate, "创建用户", "system", "api"},
		{SystemUserUpdate, "更新用户", "system", "api"},
		{SystemUserDelete, "删除用户", "system", "api"},
		{SystemUserReset, "重置密码", "system", "api"},
		{SystemUserGrant, "分配用户角色", "system", "api"},
		{SystemRoleList, "查看角色", "system", "api"},
		{SystemRoleCreate, "创建角色", "system", "api"},
		{SystemRoleUpdate, "更新角色", "system", "api"},
		{SystemRoleDelete, "删除角色", "system", "api"},
		{SystemRoleGrant, "角色授权", "system", "api"},
		{SystemMenuList, "查看菜单", "system", "api"},
		{SystemMenuCreate, "创建菜单", "system", "api"},
		{SystemMenuUpdate, "更新菜单", "system", "api"},
		{SystemMenuDelete, "删除菜单", "system", "api"},
		{SystemSessionList, "查看会话", "system", "api"},
		{SystemSessionKick, "强制下线", "system", "api"},
		{TradeOrderList, "查看订单", "trade", "api"},
		{TradeOrderRead, "查看订单详情", "trade", "api"},
		{TradeOrderStats, "查看订单统计", "trade", "api"},
		{TradeOrderPIIRead, "查看订单完整收货信息", "trade", "api"},
		{TradeOrderExport, "导出订单", "trade", "api"},
		{TradeCustomerList, "查看客户列表", "trade", "api"},
		{TradeCustomerRead, "查看客户", "trade", "api"},
		{TradeCustomerCreate, "创建客户", "trade", "api"},
		{TradeCustomerUpdate, "更新客户", "trade", "api"},
		{TradeCustomerDelete, "删除客户", "trade", "api"},
		{TradePriceListList, "查看价目表列表", "trade", "api"},
		{TradePriceListRead, "查看价目表", "trade", "api"},
		{TradePriceListCreate, "创建价目表", "trade", "api"},
		{TradePriceListUpdate, "更新价目表", "trade", "api"},
		{TradePriceListDelete, "删除价目表", "trade", "api"},
		{TradePriceListWrite, "维护价目表条目", "trade", "api"},
		{TradePriceListPreview, "预览客户价格", "trade", "api"},
		{CRMCustomerList, "查看客户", "crm", "api"},
		{CRMInquiryList, "查看询盘", "crm", "api"},
		{ShopStoreList, "查看店铺", "shop", "api"},
		{ShopStoreCreate, "创建店铺", "shop", "api"},
		{ShopStoreRead, "查看店铺详情", "shop", "api"},
		{ShopStoreUpdate, "更新店铺", "shop", "api"},
		{ShopStoreDelete, "删除店铺", "shop", "api"},
		{ShopCredentialList, "查看凭据", "shop", "api"},
		{ShopCredentialCreate, "录入凭据", "shop", "api"},
		{ShopCredentialRotate, "轮换凭据", "shop", "api"},
		{ShopCredentialCheck, "检查凭据", "shop", "api"},
		{ShopCredentialDisable, "停用凭据", "shop", "api"},
		{ShopCredentialAudit, "查看凭据审计", "shop", "api"},
		{SyncJobRun, "运行同步任务", "sync", "api"},
		{SyncRunList, "查看同步运行", "sync", "api"},
		{SyncRunRead, "查看同步运行详情", "sync", "api"},
		{SyncDLQList, "查看同步死信", "sync", "api"},
		{SyncDLQReplay, "重放同步死信", "sync", "api"},
		{SyncQuarantineList, "查看隔离记录", "sync", "api"},
		{SyncQuarantineReplay, "重放隔离记录", "sync", "api"},
		{SyncReplayRun, "历史范围重放", "sync", "api"},
		{SyncReplayCreate, "创建历史重放", "sync", "api"},
		{SyncReplayList, "查看历史重放列表", "sync", "api"},
		{SyncReplayRead, "查看历史重放进度", "sync", "api"},
		{SyncReplayCancel, "取消历史重放", "sync", "api"},
		{SyncDriftList, "查看结构漂移", "sync", "api"},
		{SyncDriftAck, "确认结构漂移", "sync", "api"},
		{SyncDriftResolve, "解决结构漂移", "sync", "api"},
		{SyncDriftIgnore, "忽略结构漂移", "sync", "api"},
		{SyncBaselinePromote, "提升结构基线", "sync", "api"},
		{ProductSPUList, "查看商品", "product", "api"},
		{ProductSPURead, "查看商品详情", "product", "api"},
		{ProductSPUCreate, "创建商品", "product", "api"},
		{ProductSPUUpdate, "更新商品", "product", "api"},
		{ProductSPUDelete, "删除商品", "product", "api"},
		{ProductSPUMerge, "归并商品", "product", "api"},
		{ProductSPUExport, "导出商品", "product", "api"},
		{ProductCategoryList, "查看类目", "product", "api"},
		{ProductCategoryWrite, "维护类目", "product", "api"},
		{ProductCategoryMap, "维护类目映射", "product", "api"},
	}
	return append(append(append(items, shareLinks()...), gatewayPermissions()...), billingPermissions()...)
}

func billingPermissions() []Definition {
	return []Definition{
		{BillingWalletList, "查看钱包与流水", "billing", "api"},
		{BillingWalletRecharge, "钱包充值与兑换码", "billing", "api"},
	}
}

func gatewayPermissions() []Definition {
	return []Definition{
		{GatewayClientList, "查看开放应用", "gateway", "api"},
		{GatewayClientCreate, "创建开放应用", "gateway", "api"},
		{GatewayClientUpdate, "启停开放应用", "gateway", "api"},
		{GatewayClientRotate, "轮换与吊销应用密钥", "gateway", "api"},
		{GatewayClientGrant, "授权开放应用", "gateway", "api"},
		{GatewayRouteList, "查看开放路由", "gateway", "api"},
		{GatewayRouteCreate, "登记开放能力", "gateway", "api"},
		{GatewayRouteUpdate, "启停开放路由", "gateway", "api"},
	}
}

func shareLinks() []Definition {
	return []Definition{
		{ShareLinkList, "查看分享链接", "share", "api"},
		{ShareLinkCreate, "发布分享链接", "share", "api"},
		{ShareLinkUpdate, "撤销与重新发布分享", "share", "api"},
		{CRMInquiryUpdate, "处理询价", "crm", "api"},
		{TradeInquiryPIIRead, "查看询价联系信息", "trade", "api"},
		{TradeInquiryExport, "导出询价安全元数据", "trade", "api"},
		{ShareVisitList, "查看分享访问明细", "share", "api"},
	}
}

// Registered rejects syntactically valid codes absent from the authoritative dictionary.
func Registered(code string) error {
	for _, item := range All() {
		if item.Code == code {
			return Validate(item.Module, code)
		}
	}
	return errors.New("permission is not registered")
}

// Export writes a deterministic JSON catalog, sorted by complete permission code.
func Export(out io.Writer) error {
	items := All()
	sort.Slice(items, func(i, j int) bool { return items[i].Code < items[j].Code })
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(items)
}

// Validate enforces three lowercase segments and a matching module namespace.
func Validate(module, code string) error {
	// ADR-019 explicitly reserves this four-segment capability.
	if module == "trade" && (code == TradeOrderPIIRead || code == TradeInquiryPIIRead) {
		return nil
	}
	parts := strings.Split(code, ":")
	if len(parts) != 3 || parts[0] != module {
		return errors.New("permission code must have three segments matching its module")
	}
	for _, part := range parts {
		if !segment.MatchString(part) {
			return errors.New("permission segments must be lowercase identifiers")
		}
	}
	return nil
}
