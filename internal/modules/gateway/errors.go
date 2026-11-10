package gateway

import (
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

func authenticationFailure(code int) error { return errs.New(code, 401, "应用凭证不可用") }

// Invalid rejects syntax without echoing submitted values.
func Invalid() error { return errs.New(10001, 400, "请求参数无效") }

// Unavailable fails closed when security dependencies cannot be checked.
func Unavailable() error { return errs.New(10002, 503, "服务暂不可用") }

// RouteUnavailable conflates absent, private and disabled capabilities.
func RouteUnavailable() error { return errs.New(80201, 404, "接口不可用") }

// ResourceUnavailable conflates missing and inaccessible resources.
func ResourceUnavailable() error { return errs.New(10003, 404, "资源不存在或不可访问") }

// PublicError maps open errors without copying internal details or causes.
func PublicError(err error) error {
	var limited *LimitError
	if errors.As(err, &limited) {
		switch limited.Decision.LimitType {
		case "global", "client", "route", "client_route", "daily_quota", "monthly_quota":
			return limited.Public()
		}
	}
	app := errs.From(err)
	switch app.Code {
	case 10001:
		if app.HTTPStatus == 413 {
			return errs.New(10001, 413, "请求体超过限制")
		}
		return Invalid()
	case 80101, 80102, 80103:
		return authenticationFailure(app.Code)
	case 80104, 20107:
		return errs.New(80104, 403, "未获授权访问此接口")
	case 80105:
		return errs.New(80105, 403, "当前来源未获授权")
	case 80201:
		return RouteUnavailable()
	case 40101, 50101, 30101, 10003, 20108:
		return ResourceUnavailable()
	case 10006:
		return errs.New(10006, 409, "幂等键与请求不一致")
	case 10004:
		return errs.New(10004, 409, "请求正在处理")
	case 90101:
		return errs.New(90101, 404, "钱包不存在")
	case 90102:
		return errs.New(90102, 402, "余额不足")
	case 90103:
		return errs.New(90103, 403, "钱包已停用")
	case 90104:
		return errs.New(90104, 409, "冻结金额不足")
	case 90301:
		return errs.New(90301, 400, "兑换码无效或已使用")
	case 10409:
		return errs.New(10409, 409, "资源版本冲突")
	case 10005:
		return errs.New(10005, 429, "请求过于频繁")
	case 30301, 30302, 30303, 60401, 60403:
		return errs.New(80301, 502, "服务暂不可用")
	case 60402:
		return errs.New(80301, 503, "服务暂不可用")
	case 80301:
		if app.HTTPStatus == 503 {
			return errs.New(80301, 503, "服务暂不可用")
		}
		return errs.New(80301, 502, "服务暂不可用")
	case 10007, 80302:
		return errs.New(80302, 504, "服务响应超时")
	case 10002:
		if app.HTTPStatus == 503 {
			return Unavailable()
		}
	}
	return errs.New(10002, 500, "服务处理失败")
}
