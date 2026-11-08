package httpx

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
)

// SystemAuthentication installs live authentication for internal routes.
// Its zero value refuses access when authentication assembly is absent.
type SystemAuthentication struct {
	guard atomic.Pointer[gin.HandlerFunc]
}

// Use installs live authentication without caching an authorization result.
func (s *SystemAuthentication) Use(handler gin.HandlerFunc) {
	if s == nil {
		return
	}
	if handler == nil {
		s.guard.Store(nil)
		return
	}
	s.guard.Store(&handler)
}

func (s *SystemAuthentication) handle(c *gin.Context) {
	if s != nil {
		if guard := s.guard.Load(); guard != nil {
			(*guard)(c)
			return
		}
	}
	c.Abort()
	Fail(c, errs.New(errs.ErrPermissionDenied, http.StatusForbidden, "无权限"))
}
