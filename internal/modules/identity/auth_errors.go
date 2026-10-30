package identity

import (
	"net/http"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// These codes already belong to the identity contract in conventions §3.2.
const (
	invalidCredentialsCode = 20101
	tokenExpiredCode       = 20102
	tokenRevokedCode       = 20103
	accountLockedCode      = 20104
)

func invalidCredentials() error {
	return errs.New(invalidCredentialsCode, http.StatusUnauthorized, "用户名或密码错误")
}
func expiredToken() error { return errs.New(tokenExpiredCode, http.StatusUnauthorized, "token 过期") }
func revokedToken() error {
	return errs.New(tokenRevokedCode, http.StatusUnauthorized, "token 已失效")
}
func lockedAccount() error { return errs.New(accountLockedCode, http.StatusLocked, "账号锁定") }
func refreshInFlight() error {
	return errs.New(errs.ErrRefreshInFlight, http.StatusConflict, "刷新处理中，请等待已发起的刷新结果")
}
func dependencyFailure(cause error) error {
	return &errs.AppError{Code: errs.ErrInternal, HTTPStatus: http.StatusServiceUnavailable, Message: "依赖不可用", Cause: cause}
}
