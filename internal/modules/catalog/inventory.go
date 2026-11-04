package catalog

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

// InventoryQuery selects a bounded page of observed platform SKU quantities.
type InventoryQuery struct {
	ShopID, ListingSKUID int64
	Page, PageSize       int
}

// InventoryStore is the authorized snapshot history read port.
type InventoryStore interface {
	Authorize(context.Context, int64, int64, string) (context.Context, error)
	ListInventories(context.Context, int64, InventoryQuery) ([]InventorySnapshot, int64, error)
}

// InventoryRoutes returns the inventory history endpoint and its permission.
func InventoryRoutes() []CategoryRoute {
	return []CategoryRoute{{"GET", "/inventories", permission.ProductSPURead}}
}

// RegisterInventoryRoutes installs authenticated inventory history reads.
func RegisterInventoryRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, store InventoryStore) error {
	if router == nil || auth == nil || profiles == nil || store == nil {
		return errors.New("inventory route dependencies required")
	}
	guard, err := identity.RequirePerm(profiles, permission.ProductSPURead)
	if err != nil {
		return err
	}
	router.GET("/api/v1/admin/inventories", identity.JWTAuth(auth), guard, func(c *gin.Context) {
		actor := identity.AdminActor(c)
		ctx, err := store.Authorize(c.Request.Context(), actor.User.TenantID, actor.User.ID, permission.ProductSPURead)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		query, err := inventoryQuery(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		rows, total, err := store.ListInventories(ctx, actor.User.TenantID, query)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"list": rows, "total": total, "page": query.Page, "page_size": query.PageSize})
	})
	return nil
}

func inventoryQuery(c *gin.Context) (InventoryQuery, error) {
	page, size, err := categoryPage(c)
	if err != nil {
		return InventoryQuery{}, err
	}
	query := InventoryQuery{Page: page, PageSize: size}
	for key, target := range map[string]*int64{"shop_id": &query.ShopID, "listing_sku_id": &query.ListingSKUID} {
		if value, ok := c.GetQuery(key); ok {
			*target, err = categoryID(value)
			if err != nil {
				return InventoryQuery{}, err
			}
		}
	}
	return query, nil
}
