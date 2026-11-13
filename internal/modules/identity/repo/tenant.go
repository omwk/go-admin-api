package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// Tenant owns directory and template persistence; the caller supplies transaction context.
type Tenant interface {
	Tenant(context.Context, int64, bool) (model.Tenant, error)
	Tenants(context.Context, int64, Page) ([]model.Tenant, int64, error)
	CreateTenant(context.Context, *model.Tenant) error
	UpdateTenant(context.Context, int64, string, string) error
	Template(context.Context, int64, bool) (model.TenantTemplate, error)
	Templates(context.Context, Page) ([]model.TenantTemplate, int64, error)
	CreateTemplate(context.Context, *model.TenantTemplate) error
	UpdateTemplate(context.Context, int64, string, string) error
	DeleteTemplate(context.Context, int64) error
	TemplateUsed(context.Context, int64) (bool, error)
	Snapshot(context.Context, int64) (model.TenantSnapshot, error)
	Materialize(context.Context, int64, model.TenantSnapshot) (int64, []int64, error)
	CreateTenantAdmin(context.Context, *model.User, []int64, *model.AuthToken) error
}

// TenantGate serializes login/refresh/invite acceptance with tenant suspension.
type TenantGate interface {
	CheckTenant(context.Context, int64) error
}
