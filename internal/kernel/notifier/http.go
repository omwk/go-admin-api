package notifier

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// InboxPort keeps persistence and authorization out of HTTP handlers.
type InboxPort interface {
	Inbox(context.Context, int64, int64, int, int, InboxFilter) (any, error)
	UnreadCount(context.Context, int64, int64) (int64, error)
	ReadAll(context.Context, int64, int64) (int64, error)
	Read(context.Context, int64, int64, int64) error
	Preferences(context.Context, int64, int64) ([]PreferenceView, error)
	SavePreferences(context.Context, int64, int64, []Preference) ([]PreferenceView, error)
	Settings(context.Context, int64, int64) (SettingsView, error)
	SaveSettings(context.Context, int64, int64, Settings) error
	Write(context.Context, int64, int64, *idem.Operation, func(context.Context) (any, error)) (idem.Result, error)
}

// HTTPAccess supplies the application's authenticated identity adapter.
type HTTPAccess struct {
	Authenticate     gin.HandlersChain
	ListPermission   gin.HandlerFunc
	ManagePermission gin.HandlerFunc
	Actor            func(*gin.Context) (int64, int64)
}

// RegisterRoutes installs inbox/read and protected channel-settings endpoints.
func RegisterRoutes(router *gin.Engine, port InboxPort, access HTTPAccess) error {
	if router == nil || port == nil || len(access.Authenticate) == 0 || access.ListPermission == nil || access.ManagePermission == nil || access.Actor == nil {
		return errors.New("notification HTTP dependencies required")
	}
	group := router.Group("/api/v1/admin", access.Authenticate...)
	group.GET("/notifications", access.ListPermission, func(c *gin.Context) {
		page, size, err := notificationPage(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		filter, err := notificationFilter(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		tenant, user := access.Actor(c)
		result, err := port.Inbox(c.Request.Context(), tenant, user, page, size, filter)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	})
	registerInboxWrites(group, port, access)
	registerNotificationSettings(group, port, access)
	return nil
}

func registerInboxWrites(group *gin.RouterGroup, port InboxPort, access HTTPAccess) {
	group.GET("/notifications/unread-count", access.ListPermission, func(c *gin.Context) {
		tenant, user := access.Actor(c)
		count, err := port.UnreadCount(c.Request.Context(), tenant, user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"count": count})
	})
	group.POST("/notifications/read-all", notificationWrite(port, access, func(ctx context.Context, _ *gin.Context, tenant, user int64) (any, error) {
		updated, err := port.ReadAll(ctx, tenant, user)
		return gin.H{"updated": updated}, err
	}))
	group.PUT("/notifications/:id/read", notificationWrite(port, access, func(ctx context.Context, c *gin.Context, tenant, user int64) (any, error) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			return nil, errs.New(10001, 400, "通知编号无效")
		}
		return struct{}{}, port.Read(ctx, tenant, user, id)
	}))
	group.GET("/notifications/preferences", func(c *gin.Context) {
		tenant, user := access.Actor(c)
		rows, err := port.Preferences(c.Request.Context(), tenant, user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"preferences": rows})
	})
	group.PUT("/notifications/preferences", notificationWrite(port, access, func(ctx context.Context, c *gin.Context, tenant, user int64) (any, error) {
		var input struct {
			Preferences []Preference `json:"preferences"`
		}
		if c.ShouldBindJSON(&input) != nil {
			return nil, errs.New(10001, 400, "通知偏好无效")
		}
		rows, err := port.SavePreferences(ctx, tenant, user, input.Preferences)
		return gin.H{"preferences": rows}, err
	}))
}

func registerNotificationSettings(group *gin.RouterGroup, port InboxPort, access HTTPAccess) {
	group.GET("/notification-settings", access.ManagePermission, func(c *gin.Context) {
		tenant, user := access.Actor(c)
		result, err := port.Settings(c.Request.Context(), tenant, user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, result)
	})
	group.PUT("/notification-settings", access.ManagePermission, notificationWrite(port, access, func(ctx context.Context, c *gin.Context, tenant, user int64) (any, error) {
		var input Settings
		if err := c.ShouldBindJSON(&input); err != nil {
			return nil, errs.New(10001, 400, "通知配置无效")
		}
		return input.View(), port.SaveSettings(ctx, tenant, user, input)
	}))
}

func notificationFilter(c *gin.Context) (InboxFilter, error) {
	filter := InboxFilter{Template: c.Query("template"), Channel: c.DefaultQuery("channel", "inbox")}
	if filter.Channel != "inbox" && !slices.Contains(ExternalChannels(), filter.Channel) {
		return filter, errs.New(10001, 400, "通知渠道无效")
	}
	if raw, set := c.GetQueryArray("unread"); set {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return filter, errs.New(10001, 400, "未读筛选无效")
		}
		value := raw[0] == "true"
		filter.Unread = &value
	}
	return filter, nil
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
