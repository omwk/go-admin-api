package channel

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

func registerDeliveries(router *gin.Engine, writes *gin.RouterGroup, m Management, a Access) {
	read := router.Group("/api/v1/admin/notifications", a.Authenticate...)
	read.GET("/deliveries", a.ListPermission, func(c *gin.Context) {
		var filter DeliveryFilter
		if c.ShouldBindQuery(&filter) != nil {
			httpx.Fail(c, errs.New(10001, 400, "投递筛选参数无效"))
			return
		}
		listHandler(func(ctx context.Context, actor Actor, page, size int) (any, error) {
			return m.ListDeliveries(ctx, actor, page, size, filter)
		}, a)(c)
	})
	writes.POST("/deliveries/:id/retry", func(c *gin.Context) {
		id, ok := objectID(c)
		if !ok {
			return
		}
		write(c, m, a, 200, false, func(ctx context.Context, actor Actor) (any, error) { return m.Retry(ctx, actor, id) })
	})
	writes.POST("/deliveries/retry", func(c *gin.Context) {
		var in struct {
			IDs []int64 `json:"ids" binding:"required,min=1,max=100,unique,dive,gt=0"`
		}
		if c.ShouldBindJSON(&in) != nil {
			httpx.Fail(c, errs.New(10001, 400, "投递编号需为 1 至 100 个不重复的正整数"))
			return
		}
		write(c, m, a, 200, false, func(ctx context.Context, actor Actor) (any, error) { return m.RetryBatch(ctx, actor, in.IDs) })
	})
}
