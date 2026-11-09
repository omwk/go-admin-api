package notifier

import (
	"context"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

// InboxPort keeps persistence and authorization out of HTTP handlers.
type InboxPort interface {
	Inbox(context.Context, int64, int64, int, int) (any, error)
	Read(context.Context, int64, int64, int64) error
	Settings(context.Context, int64, int64) (SettingsView, error)
	SaveSettings(context.Context, int64, int64, Settings) error
}

// HTTPAccess supplies the application's authenticated identity adapter.
type HTTPAccess struct {
	Authenticate       gin.HandlersChain
	SettingsPermission gin.HandlerFunc
	Actor              func(*gin.Context) (int64, int64)
}

// RegisterRoutes installs inbox/read and protected channel-settings endpoints.
func RegisterRoutes(router *gin.Engine, port InboxPort, access HTTPAccess) error {
	if router == nil || port == nil || len(access.Authenticate) == 0 || access.SettingsPermission == nil || access.Actor == nil {
		return errors.New("notification HTTP dependencies required")
	}
	group := router.Group("/api/v1/admin", access.Authenticate...)
	group.GET("/notifications", func(c *gin.Context) {
		page, size, err := notificationPage(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		tenant, user := access.Actor(c)
		result, err := port.Inbox(c.Request.Context(), tenant, user, page, size)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	})
	group.PUT("/notifications/:id/read", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			httpx.Fail(c, errs.New(10001, 400, "通知编号无效"))
			return
		}
		tenant, user := access.Actor(c)
		if err := port.Read(c.Request.Context(), tenant, user, id); err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, struct{}{})
	})
	group.GET("/notification-settings", access.SettingsPermission, func(c *gin.Context) {
		tenant, user := access.Actor(c)
		result, err := port.Settings(c.Request.Context(), tenant, user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	})
	group.PUT("/notification-settings", access.SettingsPermission, func(c *gin.Context) {
		var input Settings
		if err := c.ShouldBindJSON(&input); err != nil {
			httpx.Fail(c, errs.New(10001, 400, "通知配置无效"))
			return
		}
		tenant, user := access.Actor(c)
		if err := port.SaveSettings(c.Request.Context(), tenant, user, input); err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, input.View())
	})
	return nil
}

func notificationPage(c *gin.Context) (int, int, error) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		return 0, 0, errs.New(10001, 400, "分页参数无效")
	}
	size, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || size < 1 || size > 100 || page > 1000000 {
		return 0, 0, errs.New(10001, 400, "分页参数无效")
	}
	return page, size, nil
}
