package errs

import "sort"

// Definition is the public, enumerable error-code contract.
type Definition struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	HTTP    int    `json:"http"`
	Domain  string `json:"domain"`
}

var definitions []Definition

// Registered application codes retain their original numeric values. Registration is the only metadata source.
var (
	ErrInvalidParam        = register(10001, "参数校验失败", 400, "common")
	ErrInternal            = register(10002, "服务内部错误", 500, "common")
	ErrNotFound            = register(10003, "资源不存在", 404, "common")
	ErrConflict            = register(10004, "资源冲突", 409, "common")
	ErrTooManyRequests     = register(10005, "触发限流", 429, "common")
	ErrIdempotencyMismatch = register(10006, "幂等键请求内容不一致", 409, "common")
	ErrUpstreamTimeout     = register(10007, "上游超时", 504, "common")
	ErrVersionConflict     = register(10409, "版本冲突", 409, "common")
	ErrPermissionDenied    = register(20107, "无权限", 403, "identity")
	ErrDataScopeDenied     = register(20108, "超出数据权限", 403, "identity")
	ErrRefreshInFlight     = register(20109, "刷新正在进行", 409, "identity")
	ErrTenantSuspended     = register(20111, "租户已停用", 403, "identity")
	ErrShopNotFound        = register(30101, "店铺不存在", 404, "shop")
	ErrShopDuplicate       = register(30201, "同平台店铺已存在", 409, "shop")
)

func register(code int, message string, http int, domain string) int {
	definitions = append(definitions, Definition{Code: code, Message: message, HTTP: http, Domain: domain})
	return code
}

// All returns an owned, ascending catalog built by the same declarations used by callers.
func All() []Definition {
	result := append([]Definition(nil), definitions...)
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}
