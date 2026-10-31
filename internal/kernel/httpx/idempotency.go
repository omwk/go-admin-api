package httpx

import (
	"bytes"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

const idempotencyBodyLimit = 1 << 20

// ExtractIdempotency reads request metadata for a service-owned idempotency
// flow. The body is restored before returning so the normal decoder sees the
// exact same bytes. It does not hash or persist the client key.
func ExtractIdempotency(c *gin.Context) (idem.Input, bool, error) {
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		return idem.Input{}, false, nil
	}
	route := c.FullPath()
	if route == "" {
		return idem.Input{}, false, errs.New(errs.ErrInvalidParam, 400, "参数校验失败")
	}
	var reader io.Reader = bytes.NewReader(nil)
	if c.Request.Body != nil {
		reader = c.Request.Body
	}
	body, err := io.ReadAll(io.LimitReader(reader, idempotencyBodyLimit+1))
	if err != nil || len(body) > idempotencyBodyLimit {
		return idem.Input{}, false, errs.New(errs.ErrInvalidParam, 413, "请求体超过限制或无法读取")
	}
	if c.Request.Body != nil {
		if err := c.Request.Body.Close(); err != nil {
			return idem.Input{}, false, errs.New(errs.ErrInvalidParam, 400, "请求体无法关闭")
		}
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	params := make([]string, len(c.Params))
	for i, param := range c.Params {
		params[i] = param.Value
	}
	return idem.Input{Key: key, Method: c.Request.Method, Route: route, PathParams: params, Body: body}, true, nil
}
