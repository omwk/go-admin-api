package notifier

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

func notificationWrite(port InboxPort, access HTTPAccess, fn func(context.Context, *gin.Context, int64, int64) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, user := access.Actor(c)
		input, enabled, err := httpx.ExtractIdempotency(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var op *idem.Operation
		if enabled {
			prepared, err := idem.Prepare(tenant, user, input)
			if err != nil {
				httpx.Fail(c, errs.New(10001, 400, "幂等请求无效"))
				return
			}
			op = &prepared
		}
		result, err := port.Write(c.Request.Context(), tenant, user, op, func(ctx context.Context) (any, error) { return fn(ctx, c, tenant, user) })
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var value any
		if json.Unmarshal(result.Body, &value) != nil {
			httpx.Fail(c, errs.New(10002, 500, "通知响应无效"))
			return
		}
		httpx.OK(c, value)
	}
}
