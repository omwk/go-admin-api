package httpx

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

// Recovery converts panics to safe errors without logging their private values.
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				ctx := c.Request.Context()
				logger.From(ctx).ErrorContext(ctx, "request panic", "module", "http", "action", "recover", "error_code", errs.ErrInternal)
				if !c.Writer.Written() {
					Fail(c, errs.New(errs.ErrInternal, http.StatusInternalServerError, "内部错误"))
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	}
}

// AccessLog records only route templates and response metadata.
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		ctx := c.Request.Context()
		logger.From(ctx).InfoContext(ctx, "HTTP request", "module", "http", "action", "request",
			"method", c.Request.Method, "route", routeLabel(c), "status", responseStatus(c),
			"duration_ms", time.Since(start).Milliseconds())
	}
}

// CORS allows public cross-origin requests without credential forwarding.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id, Idempotency-Key")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// BodyLimit checks both declared and chunked bodies before the handler runs.
func BodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil {
			c.Next()
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
		closeErr := c.Request.Body.Close()
		if err != nil {
			Fail(c, errs.New(errs.ErrInvalidParam, http.StatusRequestEntityTooLarge, "请求体超过限制或无法读取"))
			return
		}
		if closeErr != nil {
			Fail(c, errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "请求体无法关闭"))
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}

func routeLabel(c *gin.Context) string {
	if path := c.FullPath(); path != "" {
		return path
	}
	return "unmatched"
}

func responseStatus(c *gin.Context) int {
	if c.Request.Context().Err() != nil {
		return http.StatusGatewayTimeout
	}
	return c.Writer.Status()
}
