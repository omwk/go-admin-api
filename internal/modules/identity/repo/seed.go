package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// SeedMenu identifies a catalog menu and its parent through stable internal paths.
type SeedMenu struct {
	Row        model.Menu
	ParentPath string
}

// SeedRole links one catalog role to codes and menu paths within the seed tenant.
type SeedRole struct {
	Row         model.Role
	Permissions []string
	MenuPaths   []string
}

// AdminSeed carries a generated hash, never the plaintext environment password.
type AdminSeed struct{ Username, DepartmentName, RoleCode, PasswordHash string }

// Seed is the dedicated tenant-bound port for initialization, without CRUD handlers.
type Seed interface {
	Transaction(context.Context, func(context.Context) error) error
	LockSeed(context.Context, int64) error
	SeedPermissions(context.Context, int64, []model.Permission) error
	SeedMenus(context.Context, int64, []SeedMenu) error
	SeedRoles(context.Context, int64, []SeedRole) error
	SeedAdmin(context.Context, int64, AdminSeed) error
}
