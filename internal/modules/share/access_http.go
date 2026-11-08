package share

import (
	"crypto/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

// RegisterPublicRoutes installs the approved anonymous content and inquiry endpoints.
// Tenant is selected by bootstrap, never from a request header, Host or query.
func RegisterPublicRoutes(router *gin.Engine, svc *LinkService, tenant int64) {
	group := router.Group("/api/v1/share")
	group.Use(publicHeaders())
	group.GET("/:token", browseHandler(svc, tenant, false))
	group.POST("/:token/verify", func(c *gin.Context) {
		var in struct {
			Password string `json:"password"`
		}
		if err := body(c, &in); err != nil {
			publicFailure(c, passwordRequired())
			return
		}
		value, expires, err := svc.Verify(c.Request.Context(), tenant, c.Param("token"), c.ClientIP(), in.Password)
		if err != nil {
			publicFailure(c, err)
			return
		}
		setBrowserCookie(c, value, expires, svc.now().UTC())
		awaitPublicFloor(c)
		httpx.OK(c, struct {
			Verified bool `json:"verified"`
		}{true})
	})
	group.GET("/:token/items/:spu_id", browseHandler(svc, tenant, true))
	group.POST("/:token/inquiry", inquiryHandler(svc, tenant))
}

func publicHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		// Fixed route-wide floor reduces the indexed lookup timing oracle.
		var jitter [1]byte
		if _, err := rand.Read(jitter[:]); err != nil {
			publicFailure(c, unavailable())
			return
		}
		c.Set("share_response_ready", time.Now().Add(time.Duration(20+int(jitter[0])%11)*time.Millisecond))
	}
}

func browseHandler(svc *LinkService, tenant int64, detail bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		spu := int64(0)
		if detail {
			var err error
			spu, err = strconv.ParseInt(c.Param("spu_id"), 10, 64)
			if err != nil || spu <= 0 {
				publicFailure(c, AccessDenied())
				return
			}
		}
		cookie, err := c.Cookie("share_access")
		if err != nil {
			cookie = ""
		}
		out, value, expires, err := svc.Browse(c.Request.Context(), tenant, c.Param("token"), c.ClientIP(), cookie, spu)
		if err != nil {
			publicFailure(c, err)
			return
		}
		if !detail {
			setBrowserCookie(c, value, expires, svc.now().UTC())
		}
		awaitPublicFloor(c)
		httpx.OK(c, out)
	}
}

func setBrowserCookie(c *gin.Context, value string, expires, now time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{Name: "share_access", Value: value, Path: "/api/v1/share", MaxAge: int(expires.Sub(now) / time.Second), Expires: expires,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func publicFailure(c *gin.Context, err error) {
	awaitPublicFloor(c)
	app := errs.From(err)
	if app.HTTPStatus == 429 {
		c.Header("Retry-After", "60")
	}
	c.AbortWithStatusJSON(app.HTTPStatus, struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
		TraceID string `json:"trace_id"`
	}{app.Code, "链接暂不可用", nil, logger.TraceID(c.Request.Context())})
}

func awaitPublicFloor(c *gin.Context) {
	value, ok := c.Get("share_response_ready")
	if !ok {
		return
	}
	ready, ok := value.(time.Time)
	if !ok {
		return
	}
	timer := time.NewTimer(time.Until(ready))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-c.Request.Context().Done():
	}
}
