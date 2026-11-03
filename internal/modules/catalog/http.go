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
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// CategoryManagementStore provides tenant-scoped category mapping operations.
type CategoryManagementStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	ListCategories(context.Context, int64) ([]Category, error)
	ListCategoryMappingPool(context.Context, int64, int64, int, int) ([]MappingPoolItem, int64, error)
	SetManualCategoryMapping(context.Context, int64, int64, int64) error
	AutoApplyCategoryMappings(context.Context, int64, int64) (int64, error)
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
		switch route.Path {
		case "/categories":
			rows, err := store.ListCategories(ctx, actor.User.TenantID)
			if err == nil {
				httpx.OK(c, rows)
				return
			}
		case "/category-mappings":
			platformID, parseErr := categoryPlatformID(c)
			if parseErr == nil {
				page, size, pageErr := categoryPage(c)
				if pageErr != nil {
					err = pageErr
					break
				}
				var total int64
				rows, total, err := store.ListCategoryMappingPool(ctx, actor.User.TenantID, platformID, page, size)
				if err == nil {
					httpx.OK(c, gin.H{"list": rows, "total": total, "page": page, "page_size": size})
					return
				}
			} else {
				err = parseErr
			}
		case "/category-mappings/:id":
			mappingID, parseErr := categoryID(c.Param("id"))
			var input struct {
				CategoryID int64 `json:"category_id"`
			}
			if parseErr == nil {
				parseErr = categoryBody(c, &input)
			}
			if parseErr == nil {
				if input.CategoryID <= 0 {
					parseErr = categoryInvalid()
				} else {
					parseErr = store.SetManualCategoryMapping(ctx, actor.User.TenantID, mappingID, input.CategoryID)
				}
			}
			err = parseErr
			if err == nil {
				httpx.OK(c, gin.H{"id": mappingID, "category_id": input.CategoryID, "source": "manual"})
				return
			}
		case "/category-mappings:auto":
			var input struct {
				PlatformID int64 `json:"platform_id"`
			}
			if err := categoryBody(c, &input); err != nil || input.PlatformID <= 0 {
				if err == nil {
					err = categoryInvalid()
				}
				httpx.Fail(c, err)
				return
			}
			count, err := store.AutoApplyCategoryMappings(ctx, actor.User.TenantID, input.PlatformID)
			if err == nil {
				httpx.OK(c, gin.H{"applied": count})
				return
			}
		}
		httpx.Fail(c, err)
	}
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
