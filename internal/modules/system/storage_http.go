package system

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/infra/storage"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// StorageRoutes declares the platform storage management contract.
func StorageRoutes() []Route {
	return []Route{{"GET", "/admin/storage-providers", permission.SystemStorageManage}, {"POST", "/admin/storage-providers", permission.SystemStorageManage}, {"GET", "/admin/storage-providers/:id", permission.SystemStorageManage}, {"PUT", "/admin/storage-providers/:id", permission.SystemStorageManage}, {"DELETE", "/admin/storage-providers/:id", permission.SystemStorageManage}, {"POST", "/admin/storage-providers/test", permission.SystemStorageManage}, {"POST", "/admin/storage-providers/:id/test", permission.SystemStorageManage}, {"POST", "/admin/storage-providers/:id/default", permission.SystemStorageManage}}
}

// RegisterStorageRoutes hides provider operations from non-platform administrators.
func RegisterStorageRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *StorageService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("storage HTTP dependencies required")
	}
	guard, err := identity.RequireActor(profiles)
	if err != nil {
		return err
	}
	group := router.Group("/api/v1", identity.JWTAuth(auth), guard, func(c *gin.Context) {
		a := identity.AdminActor(c)
		if a.User.TenantID != 1 || (!a.User.IsSuper && !slices.Contains(a.Permissions, permission.SystemStorageManage)) {
			httpx.Fail(c, notFound())
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	for _, route := range StorageRoutes() {
		group.Handle(route.Method, route.Path, storageHandler(service))
	}
	return nil
}

func storageHandler(service *StorageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		a, id := actor(c), int64(0)
		if raw := c.Param("id"); raw != "" {
			var err error
			id, err = strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				httpx.Fail(c, invalidHTTP())
				return
			}
		}
		if c.Request.Method == "GET" {
			var value any
			var err error
			if id == 0 {
				value, err = service.List(c.Request.Context(), a)
			} else {
				value, err = service.Get(c.Request.Context(), a, id)
			}
			respond(c, value, err)
			return
		}
		input, body, err := decodeStorageRequest(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		var operation *idem.Operation
		if key := c.GetHeader("Idempotency-Key"); key != "" {
			op, prepareErr := idem.Prepare(a.TenantID, a.UserID, idem.Input{Key: key, Method: c.Request.Method, Route: c.FullPath(), PathParams: []string{c.Param("id")}, Body: body})
			if prepareErr != nil {
				httpx.Fail(c, invalidHTTP())
				return
			}
			operation = &op
		}
		run, err := storageOperation(c.Request.Context(), service, a, id, c.Request.Method, c.FullPath(), input)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		result, err := service.Execute(c.Request.Context(), operation, run)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		switch result.Status {
		case 201:
			httpx.Created(c, result.Body)
		case 204:
			httpx.NoContent(c)
		default:
			httpx.OK(c, result.Body)
		}
	}
}

func decodeStorageRequest(c *gin.Context) (StorageInput, []byte, error) {
	if c.FullPath() != "/api/v1/admin/storage-providers/test" {
		return decodeRequest[StorageInput](c)
	}
	input, body, err := decodeRequest[StorageTestInput](c)
	return input.configuration(), body, err
}

func storageOperation(ctx context.Context, service *StorageService, a Actor, id int64, method, path string, input StorageInput) (func(context.Context) (idem.Result, error), error) {
	if path == "/api/v1/admin/storage-providers/:id/test" || path == "/api/v1/admin/storage-providers/test" {
		// Probe before opening the idempotency transaction; persist only the sanitized audit inside it.
		probe := func() (storage.Provider, string, error) {
			if id == 0 {
				return service.probeStorageInput(ctx, a, input)
			}
			return service.probeStorage(ctx, a, id)
		}
		provider, status, err := probe()
		if err != nil {
			return nil, err
		}
		return func(tx context.Context) (idem.Result, error) {
			if err := service.recordProvider(tx, a, "storage.provider.test", provider); err != nil {
				return idem.Result{}, err
			}
			return idem.JSONResult(200, map[string]string{"status": status})
		}, nil
	}
	return func(tx context.Context) (idem.Result, error) {
		value, status, err := storageWrite(tx, service, a, id, method, path, input)
		if err != nil {
			return idem.Result{}, err
		}
		return idem.JSONResult(status, value)
	}, nil
}

func storageWrite(ctx context.Context, service *StorageService, a Actor, id int64, method, path string, input StorageInput) (any, int, error) {
	if method == "DELETE" {
		return nil, 204, service.Delete(ctx, a, id)
	}
	if path == "/api/v1/admin/storage-providers/:id/default" {
		value, err := service.SetDefault(ctx, a, id)
		return value, 200, err
	}
	value, err := service.Save(ctx, a, id, input)
	if id == 0 {
		return value, 201, err
	}
	return value, 200, err
}
