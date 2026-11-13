package channel

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// Actor is the authenticated administrator and trusted tenant boundary.
type Actor struct {
	TenantID, UserID int64
	Super            bool
}

// ProviderInput contains write-only provider settings.
type ProviderInput struct {
	Channel  string `json:"channel"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Config   Config `json:"config"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
	Global   bool   `json:"global"`
}

// ProviderView is an explicit safe projection, including configured flags.
type ProviderView struct {
	Provider
	Config map[string]any `json:"config"`
}

// Page follows the standard management pagination contract.
type Page struct {
	List     any   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

// Management keeps storage and side effects outside the HTTP layer.
type Management interface {
	ListProviders(context.Context, Actor, int, int) (any, error)
	SaveProvider(context.Context, Actor, int64, ProviderInput) (any, error)
	DeleteProvider(context.Context, Actor, int64) error
	TestProvider(context.Context, Actor, int64) (any, error)
	ListTemplates(context.Context, Actor, int, int) (any, error)
	PutTemplate(context.Context, Actor, string, Template) (any, error)
	ListDeliveries(context.Context, Actor, int, int) (any, error)
	Retry(context.Context, Actor, int64) (any, error)
	Write(context.Context, Actor, *idem.Operation, int, bool, func(context.Context) (any, error)) (idem.Result, error)
}

// Access adapts the host application's authenticated actor and permission guards.
type Access struct {
	Authenticate   gin.HandlersChain
	Permission     gin.HandlerFunc
	ListPermission gin.HandlerFunc
	Actor          func(*gin.Context) Actor
}

// RegisterManagement installs the nine F3 operations under the standard API prefix.
func RegisterManagement(router *gin.Engine, m Management, a Access) {
	g := router.Group("/api/v1/admin/notifications", a.Authenticate...)
	g.Use(a.Permission)
	g.Use(func(c *gin.Context) {
		input, enabled, err := httpx.ExtractIdempotency(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if enabled {
			actor := a.Actor(c)
			prepared, err := idem.Prepare(actor.TenantID, actor.UserID, input)
			if err != nil {
				httpx.Fail(c, errs.New(10001, 400, "幂等请求无效"))
				return
			}
			c.Set("notification_operation", &prepared)
		}
		c.Next()
	})
	g.GET("/providers", listHandler(m.ListProviders, a))
	g.POST("/providers", func(c *gin.Context) { saveProvider(c, m, a, 0) })
	g.PUT("/providers/:id", func(c *gin.Context) {
		id, ok := objectID(c)
		if ok {
			saveProvider(c, m, a, id)
		}
	})
	g.DELETE("/providers/:id", func(c *gin.Context) {
		id, ok := objectID(c)
		if !ok {
			return
		}
		write(c, m, a, 204, false, func(ctx context.Context, actor Actor) (any, error) { return nil, m.DeleteProvider(ctx, actor, id) })
	})
	g.POST("/providers/:id/test", func(c *gin.Context) {
		id, ok := objectID(c)
		if !ok {
			return
		}
		write(c, m, a, 200, true, func(ctx context.Context, actor Actor) (any, error) { return m.TestProvider(ctx, actor, id) })
	})
	g.GET("/templates", listHandler(m.ListTemplates, a))
	g.PUT("/templates/:code", func(c *gin.Context) {
		var in Template
		if c.ShouldBindJSON(&in) != nil {
			httpx.Fail(c, errs.New(10001, 400, "模板参数无效"))
			return
		}
		write(c, m, a, 200, false, func(ctx context.Context, actor Actor) (any, error) {
			return m.PutTemplate(ctx, actor, c.Param("code"), in)
		})
	})
	read := router.Group("/api/v1/admin/notifications", a.Authenticate...)
	read.GET("/deliveries", a.ListPermission, listHandler(m.ListDeliveries, a))
	g.POST("/deliveries/:id/retry", func(c *gin.Context) {
		id, ok := objectID(c)
		if !ok {
			return
		}
		write(c, m, a, 200, true, func(ctx context.Context, actor Actor) (any, error) { return m.Retry(ctx, actor, id) })
	})
}

func saveProvider(c *gin.Context, m Management, a Access, id int64) {
	var in ProviderInput
	if c.ShouldBindJSON(&in) != nil {
		httpx.Fail(c, errs.New(10001, 400, "通道参数无效"))
		return
	}
	status := 200
	if id == 0 {
		status = 201
	}
	write(c, m, a, status, false, func(ctx context.Context, actor Actor) (any, error) { return m.SaveProvider(ctx, actor, id, in) })
}

func listHandler(fn func(context.Context, Actor, int, int) (any, error), a Access) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, e1 := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, e2 := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		if e1 != nil || e2 != nil || page < 1 || page > 1000000 || size < 1 || size > 100 {
			httpx.Fail(c, errs.New(10001, 400, "分页参数无效"))
			return
		}
		out, err := fn(c.Request.Context(), a.Actor(c), page, size)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, out)
	}
}

func objectID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errs.New(10001, 400, "编号无效"))
		return 0, false
	}
	return id, true
}

func write(c *gin.Context, m Management, a Access, status int, deliver bool, fn func(context.Context, Actor) (any, error)) {
	actor := a.Actor(c)
	var op *idem.Operation
	if value, ok := c.Get("notification_operation"); ok {
		op, _ = value.(*idem.Operation)
	}
	result, err := m.Write(c.Request.Context(), actor, op, status, deliver, func(ctx context.Context) (any, error) { return fn(ctx, actor) })
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if result.Status == 204 {
		httpx.NoContent(c)
		return
	}
	var out any
	if jsonError := decodeResult(result.Body, &out); jsonError != nil {
		httpx.Fail(c, jsonError)
		return
	}
	if result.Status == 201 {
		httpx.Created(c, out)
		return
	}
	httpx.OK(c, out)
}
