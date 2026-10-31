package shop

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/shop/repo"
)

// Route describes one shop HTTP operation and its optional permission code.
type Route struct{ Method, Path, Permission string }

// Routes returns the platform and shop CRUD route manifest.
func Routes() []Route {
	return []Route{
		{"GET", "/platforms", ""},
		{"GET", "/shops", permission.ShopStoreList}, {"POST", "/shops", permission.ShopStoreCreate},
		{"GET", "/shops/:id", permission.ShopStoreRead}, {"PUT", "/shops/:id", permission.ShopStoreUpdate}, {"DELETE", "/shops/:id", permission.ShopStoreDelete},
	}
}

// RegisterRoutes installs platform read-only and scoped shop CRUD routes.
func RegisterRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *Service) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("shop route dependencies are required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	group.GET("/platforms", func(c *gin.Context) {
		rows, err := service.ListPlatforms(c.Request.Context())
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, rows)
	})
	for _, route := range Routes()[1:] {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		handler := shopHandler{service: service}
		var fn gin.HandlerFunc
		switch route.Method + " " + route.Path {
		case "GET /shops":
			fn = handler.list
		case "POST /shops":
			fn = handler.create
		case "GET /shops/:id":
			fn = handler.get
		case "PUT /shops/:id":
			fn = handler.update
		case "DELETE /shops/:id":
			fn = handler.delete
		default:
			return errors.New("shop route handler is missing")
		}
		group.Handle(route.Method, route.Path, guard, fn)
	}
	return nil
}

type shopHandler struct{ service *Service }

func (h shopHandler) list(c *gin.Context) {
	page, err := shopPage(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.List(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, page)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h shopHandler) get(c *gin.Context) {
	id, err := shopID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.Get(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h shopHandler) create(c *gin.Context) {
	var request CreateRequest
	if err := decodeShop(c, &request); err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.Create(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, request)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.Created(c, result)
}

func (h shopHandler) update(c *gin.Context) {
	id, err := shopID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	var request UpdateRequest
	if err := decodeShop(c, &request); err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	result, err := h.service.Update(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id, request)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, result)
}

func (h shopHandler) delete(c *gin.Context) {
	id, err := shopID(c)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	a := identity.AdminActor(c)
	err = h.service.Delete(c.Request.Context(), Actor{TenantID: a.User.TenantID, UserID: a.User.ID, DepartmentID: a.User.DepartmentID}, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.NoContent(c)
}

func decodeShop(c *gin.Context, target any) error {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 || len(bytes.TrimSpace(body)) == 0 {
		return invalidShop()
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidShop()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidShop()
	}
	if binding.Validator.ValidateStruct(target) != nil {
		return invalidShop()
	}
	return nil
}

func shopID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalidShop()
	}
	return id, nil
}

func shopPage(c *gin.Context) (repo.Page, error) {
	page := repo.Page{Number: 1, Size: 20, Sort: "-created_at"}
	for key, target := range map[string]*int{"page": &page.Number, "page_size": &page.Size} {
		if raw, ok := c.GetQuery(key); ok {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return page, invalidShop()
			}
			*target = value
		}
	}
	if page.Size > 100 {
		page.Size = 100
	}
	if page.Number > 10000 {
		return page, invalidShop()
	}
	if sort, ok := c.GetQuery("sort"); ok {
		page.Sort = sort
	}
	if !map[string]bool{"-created_at": true, "created_at": true, "name": true, "-name": true, "id": true, "-id": true}[page.Sort] {
		return page, invalidShop()
	}
	return page, nil
}

func invalidShop() error { return errs.New(errs.ErrInvalidParam, 400, "参数校验失败") }
