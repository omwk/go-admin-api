package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/logger"
)

// Pinger is the dependency health contract used by readiness checks.
type Pinger interface{ Ping(context.Context) error }

// BuildInfo contains metadata injected when the API is built.
type BuildInfo struct {
	Version   string `json:"version"`
	BuildTime string `json:"build_time"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
}

// Dependencies supplies system probes and logging without infrastructure imports.
type Dependencies struct {
	DB, Redis Pinger
	Log       *slog.Logger
	Build     BuildInfo
}

// NewEngine assembles the middleware and system routes.
// Serve it through Timeout to enforce a response deadline at the HTTP boundary.
func NewEngine(deps Dependencies) (*gin.Engine, error) {
	if deps.DB == nil || deps.Redis == nil || deps.Log == nil {
		return nil, errors.New("HTTP database, redis and logger dependencies are required")
	}
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		return nil, err
	}
	m := newMetrics()
	r.Use(requestContext(deps.Log), AccessLog(), m.middleware(), Recovery(), CORS(), BodyLimitExcept(1<<20, "POST", "/api/v1/admin/files"))
	RegisterSystemRoutes(r, deps)
	r.GET("/metrics", gin.WrapH(m.handler()))
	r.NoRoute(func(c *gin.Context) { Fail(c, errs.New(errs.ErrNotFound, http.StatusNotFound, "资源不存在")) })
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		Fail(c, errs.New(errs.ErrInvalidParam, http.StatusMethodNotAllowed, "请求方法不支持"))
	})
	return r, nil
}

func requestContext(log *slog.Logger) gin.HandlerFunc {
	trace := TraceID()
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(logger.With(c.Request.Context(), log))
		if id := logger.TraceID(c.Request.Context()); id != "" {
			c.Header("X-Request-Id", id)
			if c.GetHeader("X-Request-Id") != "" {
				c.Set("request_id", c.GetHeader("X-Request-Id"))
			}
			c.Next()
			return
		}
		trace(c)
	}
}

// RegisterSystemRoutes installs liveness, cached readiness and build information.
func RegisterSystemRoutes(r *gin.Engine, deps Dependencies) {
	ready := &readiness{db: deps.DB, redis: deps.Redis}
	r.GET("/healthz", func(c *gin.Context) { OK(c, gin.H{"status": "ok"}) })
	r.GET("/readyz", ready.handle)
	build := deps.Build
	build.GoVersion = runtime.Version()
	r.GET("/api/v1/admin/system/info", func(c *gin.Context) { OK(c, build) })
}

// Serve runs the HTTP server and closes it when the process context is canceled.
func Serve(ctx context.Context, server *http.Server) error {
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("HTTP listener failed")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.Join(err, server.Close(), <-result)
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
