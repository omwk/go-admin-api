// Package repo defines the shop persistence boundary.
package repo

import (
	"context"
	"github.com/omwk/go-admin-api/internal/modules/shop/model"
)

// Page is a validated shop list request.
type Page struct {
	Number int
	Size   int
	Sort   string
}

// ShopRow combines a scoped shop with its display-only platform name.
type ShopRow struct {
	model.Shop
	PlatformName string `gorm:"column:platform_name"`
}

// Store is the persistence port for the T2.3 shop CRUD surface.
type Store interface {
	Platforms(context.Context) ([]model.Platform, error)
	Shops(context.Context, int64, Page) ([]ShopRow, int64, error)
	Shop(context.Context, int64, int64) (ShopRow, error)
	ValidateAssignment(context.Context, int64, int64, int64) error
	CreateShop(context.Context, *model.Shop) error
	UpdateShop(context.Context, int64, int64, int, map[string]any) error
	DeleteShop(context.Context, int64, int64) error
}
