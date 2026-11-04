package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// CategoryManagementStore provides tenant-scoped category mapping operations.
type CategoryManagementStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	ListCategories(context.Context, int64) ([]Category, error)
	ListCategoryMappingPool(context.Context, int64, int64, int, int, ...string) ([]MappingPoolItem, int64, error)
	SetManualCategoryMapping(context.Context, int64, int64, int64, ...*idem.Operation) error
}

// CategoryRoute describes a category management endpoint and its required permission.
type CategoryRoute struct{ Method, Path, Permission string }

// CategoryRoutes returns the category APIs owned by this module.
func CategoryRoutes() []CategoryRoute {
	return []CategoryRoute{
		{"GET", "/categories", permission.ProductCategoryList},
		{"GET", "/category-mappings", permission.ProductCategoryList},
		{"PUT", "/category-mappings/:id", permission.ProductCategoryMap},
		{"POST", "/category-mappings:auto", permission.ProductCategoryMap},
	}
}

// RegisterCategoryRoutes installs authenticated category management endpoints.
func RegisterCategoryRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, store CategoryManagementStore) error {
	if router == nil || auth == nil || profiles == nil || store == nil {
		return errors.New("category route dependencies required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range CategoryRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, categoryHandler(store, route))
	}
	return nil
}

func categoryHandler(store CategoryManagementStore, route CategoryRoute) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := identity.AdminActor(c)
		ctx, err := store.Authorize(c.Request.Context(), actor.User.TenantID, actor.User.ID, route.Permission)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Request = c.Request.WithContext(ctx)
		var out any
		switch route.Path {
		case "/categories":
			out, err = store.ListCategories(ctx, actor.User.TenantID)
		case "/category-mappings", "/category-mappings:auto":
			out, err = categoryPool(c, store, actor.User.TenantID, route.Method == "POST")
		case "/category-mappings/:id":
			out, err = categoryManual(c, store, actor.User.TenantID, actor.User.ID)
		}
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, out)
	}
}

func categoryPool(c *gin.Context, store CategoryManagementStore, tenant int64, auto bool) (any, error) {
	platform, err := categoryPlatformID(c)
	matched := c.DefaultQuery("matched", "false")
	if auto {
		var input struct {
			PlatformID int64 `json:"platform_id"`
		}
		if err := categoryBody(c, &input); err != nil || input.PlatformID < 0 {
			return nil, categoryInvalid()
		}
		platform, matched = input.PlatformID, "false"
	}
	if err != nil || (matched != "false" && matched != "true" && matched != "all") {
		return nil, categoryInvalid()
	}
	page, size, err := categoryPage(c)
	if err != nil {
		return nil, err
	}
	rows, total, err := store.ListCategoryMappingPool(c.Request.Context(), tenant, platform, page, size, matched)
	return gin.H{"list": rows, "total": total, "page": page, "page_size": size}, err
}

func categoryManual(c *gin.Context, store CategoryManagementStore, tenant, user int64) (any, error) {
	input, enabled, err := httpx.ExtractIdempotency(c)
	if err != nil {
		return nil, err
	}
	var op *idem.Operation
	if enabled {
		prepared, err := idem.Prepare(tenant, user, input)
		if err != nil {
			return nil, categoryInvalid()
		}
		op = &prepared
	}
	id, err := categoryID(c.Param("id"))
	if err != nil {
		return nil, err
	}
	var body struct {
		CategoryID int64 `json:"category_id"`
	}
	if err := categoryBody(c, &body); err != nil || body.CategoryID <= 0 {
		return nil, categoryInvalid()
	}
	err = store.SetManualCategoryMapping(c.Request.Context(), tenant, id, body.CategoryID, op)
	return gin.H{"id": id, "category_id": body.CategoryID, "source": "manual"}, err
}

func categoryPage(c *gin.Context) (int, int, error) {
	page, size := 1, 20
	for key, target := range map[string]*int{"page": &page, "page_size": &size} {
		if raw, ok := c.GetQuery(key); ok {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return 0, 0, categoryInvalid()
			}
			*target = value
		}
	}
	if size > 100 || page > 10000 || (page-1)*size >= 10000 {
		return 0, 0, categoryInvalid()
	}
	return page, size, nil
}

func categoryPlatformID(c *gin.Context) (int64, error) {
	if c.Query("platform_id") == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(c.Query("platform_id"), 10, 64)
	if err != nil || value <= 0 {
		return 0, categoryInvalid()
	}
	return value, nil
}

func categoryID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, categoryInvalid()
	}
	return id, nil
}

func categoryBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 4097))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return categoryInvalid()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return categoryInvalid()
	}
	return nil
}

func categoryInvalid() error {
	return errs.New(errs.ErrInvalidParam, http.StatusBadRequest, "参数校验失败")
}
