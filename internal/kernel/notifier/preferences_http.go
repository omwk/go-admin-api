package notifier

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

// RegisterPersonalPreferences exposes the current user's preferences under /auth.
func RegisterPersonalPreferences(router *gin.Engine, port InboxPort, access HTTPAccess) {
	g := router.Group("/api/v1/auth/notification-preferences", access.Authenticate...)
	g.GET("", func(c *gin.Context) {
		tenant, user := access.Actor(c)
		rows, err := port.Preferences(c.Request.Context(), tenant, user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"preferences": rows})
	})
	g.PUT("", notificationWrite(port, access, func(ctx context.Context, c *gin.Context, tenant, user int64) (any, error) {
		var in struct {
			Preferences []Preference `json:"preferences"`
		}
		if c.ShouldBindJSON(&in) != nil {
			return nil, errs.New(10001, 400, "通知偏好无效")
		}
		rows, err := port.SavePreferences(ctx, tenant, user, in.Preferences)
		return gin.H{"preferences": rows}, err
	}))
}
