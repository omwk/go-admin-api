package channel

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
)

// TemplatePreview is rendered with public examples, never real credentials.
type TemplatePreview struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func registerTemplateTools(g *gin.RouterGroup, m Management, a Access) {
	g.GET("/templates/variables", func(c *gin.Context) {
		out, err := m.TemplateVariables(c.Request.Context(), a.Actor(c))
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, out)
	})
	g.POST("/templates/:id/preview", func(c *gin.Context) {
		id, ok := objectID(c)
		if ok {
			write(c, m, a, 200, false, func(ctx context.Context, actor Actor) (any, error) {
				return m.PreviewTemplate(ctx, actor, id)
			})
		}
	})
	g.POST("/templates/:id/test", func(c *gin.Context) {
		id, ok := objectID(c)
		if ok {
			write(c, m, a, 200, true, func(ctx context.Context, actor Actor) (any, error) {
				return m.TestTemplate(ctx, actor, id)
			})
		}
	})
}
