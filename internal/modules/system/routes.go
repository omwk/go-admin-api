package system

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// Route declares one operation and its permission; an empty permission requires login only.
type Route struct{ Method, Path, Permission string }

// Routes is the complete S2 HTTP manifest, including login-only metadata.
func Routes() []Route {
	return []Route{
		{"GET", "/admin/settings", permission.SystemSettingList},
		{"GET", "/admin/settings/:group", permission.SystemSettingList},
		{"PUT", "/admin/settings/:group", permission.SystemSettingUpdate},
		{"GET", "/admin/dicts", permission.SystemDictList}, {"POST", "/admin/dicts", permission.SystemDictCreate},
		{"PUT", "/admin/dicts/:id", permission.SystemDictUpdate}, {"DELETE", "/admin/dicts/:id", permission.SystemDictDelete},
		{"GET", "/admin/dicts/:id/items", permission.SystemDictList}, {"POST", "/admin/dicts/:id/items", permission.SystemDictCreate},
		{"PUT", "/admin/dict-items/:id", permission.SystemDictUpdate}, {"DELETE", "/admin/dict-items/:id", permission.SystemDictDelete},
		{"GET", "/meta/dicts/:type", ""}, {"GET", "/meta/error-codes", ""},
	}
}

// WriteFunc runs service writes and optional idempotency inside one authorized transaction.
type WriteFunc func(context.Context, Actor, string, string, string, func(context.Context) (idem.Result, error)) (idem.Result, error)

type handler struct {
	settings *SettingService
	dicts    *DictService
	write    WriteFunc
}

// RegisterRoutes attaches settings, dictionaries and metadata to the existing identity guards.
func RegisterRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, settings *SettingService, dicts *DictService, write WriteFunc) error {
	if router == nil || auth == nil || profiles == nil || settings == nil || dicts == nil || write == nil {
		return errors.New("system HTTP dependencies are required")
	}
	h := handler{settings: settings, dicts: dicts, write: write}
	handlers := h.handlers()
	group := router.Group("/api/v1", identity.JWTAuth(auth))
	for _, route := range Routes() {
		var guard gin.HandlerFunc
		var err error
		if route.Permission == "" {
			guard, err = identity.RequireActor(profiles)
		} else {
			guard, err = identity.RequirePerm(profiles, route.Permission)
		}
		if err != nil {
			return err
		}
		fn := handlers[route.Method+" "+route.Path]
		if fn == nil {
			return errors.New("system route handler is missing")
		}
		group.Handle(route.Method, route.Path, guard, fn)
	}
	return nil
}
