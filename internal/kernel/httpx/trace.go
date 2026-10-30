package httpx

import (
	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

// TraceID propagates a client request identifier or generates a new ULID.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" {
			id = ulid.Make().String()
		} else {
			c.Set("request_id", id)
		}
		c.Request = c.Request.WithContext(logger.WithTraceID(c.Request.Context(), id))
		c.Header("X-Request-Id", id)
		c.Next()
	}
}
