package channel

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// Audience selects active recipients within the caller's authorized tenants.
type Audience struct {
	Type string  `json:"type"`
	IDs  []int64 `json:"ids,omitempty"`
}

// BroadcastInput contains the announcement and its explicit audience.
type BroadcastInput struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Channels []string `json:"channels"`
	Audience Audience `json:"audience"`
}

// BroadcastResult identifies one durable announcement.
type BroadcastResult struct {
	BroadcastID int64 `json:"broadcast_id"`
	Recipients  int64 `json:"recipients"`
}

// BroadcastView describes one historical announcement without user addresses.
type BroadcastView struct {
	BroadcastResult
	Title     string    `json:"title"`
	Channels  []string  `json:"channels"`
	Audience  Audience  `json:"audience"`
	CreatedAt time.Time `json:"created_at"`
}

// BroadcastManagement supplies the shared transaction and announcement operations.
type BroadcastManagement interface {
	PreviewBroadcast(context.Context, Actor, Audience) (any, error)
	CreateBroadcast(context.Context, Actor, BroadcastInput) (any, error)
	ListBroadcasts(context.Context, Actor, int, int) (any, error)
	Management
}

// RegisterBroadcasts gives announcements their independent permission boundary.
func RegisterBroadcasts(router *gin.Engine, m BroadcastManagement, a Access, guard gin.HandlerFunc) {
	g := router.Group("/api/v1/admin/notifications/broadcasts", a.Authenticate...)
	g.Use(guard)
	g.Use(broadcastOperation(a))
	g.GET("", listHandler(m.ListBroadcasts, a))
	g.POST("/preview", func(c *gin.Context) {
		var in struct {
			Audience Audience `json:"audience"`
		}
		if c.ShouldBindJSON(&in) != nil {
			httpx.Fail(c, errs.New(10001, 400, "公告范围无效"))
			return
		}
		write(c, m, a, 200, false, func(ctx context.Context, actor Actor) (any, error) {
			return m.PreviewBroadcast(ctx, actor, in.Audience)
		})
	})
	g.POST("", func(c *gin.Context) {
		var in BroadcastInput
		if c.ShouldBindJSON(&in) != nil {
			httpx.Fail(c, errs.New(10001, 400, "公告参数无效"))
			return
		}
		write(c, m, a, 201, false, func(ctx context.Context, actor Actor) (any, error) {
			return m.CreateBroadcast(ctx, actor, in)
		})
	})
}

func broadcastOperation(a Access) gin.HandlerFunc {
	return func(c *gin.Context) {
		input, enabled, err := httpx.ExtractIdempotency(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if enabled {
			actor := a.Actor(c)
			op, err := idem.Prepare(actor.TenantID, actor.UserID, input)
			if err != nil {
				httpx.Fail(c, errs.New(10001, 400, "幂等请求无效"))
				return
			}
			c.Set("notification_operation", &op)
		}
		c.Next()
	}
}
