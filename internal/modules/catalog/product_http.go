package catalog

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// ProductRoutes describes the product master data endpoints and permissions.
func ProductRoutes() []CategoryRoute {
	return []CategoryRoute{
		{"GET", "/products", permission.ProductSPUList},
		{"GET", "/products/:id", permission.ProductSPURead},
		{"POST", "/products", permission.ProductSPUCreate},
		{"PUT", "/products/:id", permission.ProductSPUUpdate},
		{"DELETE", "/products/:id", permission.ProductSPUDelete},
		{"GET", "/products/:id/listings", permission.ProductSPURead},
		{"POST", "/products/:id/merge", permission.ProductSPUMerge},
		{"GET", "/listings", permission.ProductSPUList},
	}
}

// RegisterProductRoutes installs authenticated product and listing endpoints.
func RegisterProductRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *ProductService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("product route dependencies required")
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth))
	for _, route := range ProductRoutes() {
		guard, err := identity.RequirePerm(profiles, route.Permission)
		if err != nil {
			return err
		}
		group.Handle(route.Method, route.Path, guard, productHTTPHandler(service, route))
	}
	return nil
}

func productHTTPHandler(service *ProductService, route CategoryRoute) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := identity.AdminActor(c)
		id, err := productPathID(c)
		if strings.Contains(route.Path, ":id") && err != nil {
			httpx.Fail(c, err)
			return
		}
		if route.Path == "/products" || route.Path == "/listings" {
			err = productCollectionHandler(c, service, route, actor.User.TenantID, actor.User.ID)
		} else {
			err = productItemHandler(c, service, route, actor.User.TenantID, actor.User.ID, id)
		}
		if err != nil {
			httpx.Fail(c, err)
		}
	}
}

func productCollectionHandler(c *gin.Context, service *ProductService, route CategoryRoute, tenant, user int64) error {
	if route.Method == "POST" {
		var input ProductCreateRequest
		if err := productBody(c, &input); err != nil {
			return err
		}
		result, err := service.Create(c.Request.Context(), tenant, user, input)
		if err == nil {
			httpx.Created(c, result)
		}
		return err
	}
	var query ProductListQuery
	if err := parseProductQuery(c, &query); err != nil {
		return err
	}
	if route.Path == "/listings" {
		result, err := service.ListingPage(c.Request.Context(), tenant, user, query)
		if err == nil {
			httpx.OK(c, result)
		}
		return err
	}
	result, err := service.List(c.Request.Context(), tenant, user, query)
	if err == nil {
		httpx.OK(c, result)
	}
	return err
}

func productItemHandler(c *gin.Context, service *ProductService, route CategoryRoute, tenant, user, id int64) error {
	switch route.Method + " " + route.Path {
	case "GET /products/:id", "GET /products/:id/listings":
		result, err := service.Get(c.Request.Context(), tenant, user, id)
		if err == nil && route.Path == "/products/:id/listings" {
			httpx.OK(c, gin.H{"list": result.Listings, "total": len(result.Listings), "page": 1, "page_size": len(result.Listings)})
		} else if err == nil {
			httpx.OK(c, result)
		}
		return err
	case "PUT /products/:id":
		var input ProductUpdateRequest
		if err := productBody(c, &input); err != nil {
			return err
		}
		result, err := service.Update(c.Request.Context(), tenant, user, id, input)
		if err == nil {
			httpx.OK(c, result)
		}
		return err
	case "DELETE /products/:id":
		version, err := strconv.Atoi(c.Query("version"))
		if err != nil || version < 0 {
			return invalidProduct()
		}
		if err := service.Archive(c.Request.Context(), tenant, user, id, version); err != nil {
			return err
		}
		httpx.OK(c, gin.H{"id": id, "archived": true})
		return nil
	case "POST /products/:id/merge":
		var input struct {
			ListingIDs []int64 `json:"listing_ids"`
		}
		if err := productBody(c, &input); err != nil {
			return err
		}
		result, err := service.Merge(c.Request.Context(), tenant, user, id, input.ListingIDs)
		if err == nil {
			httpx.OK(c, result)
		}
		return err
	default:
		return invalidProduct()
	}
}

func productPathID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalidProduct()
	}
	return id, nil
}

func productBody(c *gin.Context, target any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidProduct()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidProduct()
	}
	return nil
}

func parseProductQuery(c *gin.Context, query *ProductListQuery) error {
	values := c.Request.URL.Query()
	setString := func(key string, target *string) { *target = strings.TrimSpace(values.Get(key)) }
	setString("keyword", &query.Keyword)
	setString("status", &query.Status)
	setString("sort", &query.Sort)
	setString("cursor", &query.Cursor)
	if raw, present := values["include_descendants"]; present {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return invalidProduct()
		}
		query.IncludeDescendants = raw[0] == "true"
	}
	for key, target := range map[string]*int{"page": &query.Page, "page_size": &query.PageSize} {
		if values.Has(key) {
			parsed, err := strconv.Atoi(values.Get(key))
			if err != nil {
				return invalidProduct()
			}
			*target = parsed
		}
	}
	for key, target := range map[string]*int64{"shop_id": &query.ShopID, "platform_id": &query.PlatformID, "category_id": &query.CategoryID} {
		if values.Has(key) {
			parsed, err := strconv.ParseInt(values.Get(key), 10, 64)
			if err != nil || parsed < 0 {
				return invalidProduct()
			}
			*target = parsed
		}
	}
	for key, target := range map[string]**int64{"price_min_cents": &query.PriceMinCents, "price_max_cents": &query.PriceMaxCents} {
		if values.Has(key) {
			parsed, err := strconv.ParseInt(values.Get(key), 10, 64)
			if err != nil || parsed < 0 {
				return invalidProduct()
			}
			*target = &parsed
		}
	}
	for key, target := range map[string]**bool{"has_price": &query.HasPrice, "is_manual": &query.IsManual} {
		if values.Has(key) {
			parsed, err := strconv.ParseBool(values.Get(key))
			if err != nil {
				return invalidProduct()
			}
			*target = &parsed
		}
	}
	for key, target := range map[string]**time.Time{"updated_from": &query.UpdatedFrom, "updated_to": &query.UpdatedTo} {
		if values.Has(key) {
			parsed, err := time.Parse(time.RFC3339, values.Get(key))
			if err != nil {
				return invalidProduct()
			}
			*target = &parsed
		}
	}
	return nil
}
