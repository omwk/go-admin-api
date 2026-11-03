package repo

import (
	"context"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
)

// PermissionReleaseStore persists one-time authorization releases.
type PermissionReleaseStore interface {
	Transaction(context.Context, func(context.Context) error) error
	LockPermissionRelease(context.Context, int64) error
	RoleByCode(context.Context, int64, string) (model.Role, error)
	RolePermissionCodes(context.Context, int64, int64) ([]string, error)
	RegisterPermissions(context.Context, int64, []model.Permission) error
	PermissionIDs(context.Context, int64, []string) (map[string]int64, error)
	InsertRolePermissions(context.Context, int64, int64, []int64) error
	PermissionReleaseMarker(context.Context, int64, string) (string, bool, error)
	SavePermissionReleaseMarker(context.Context, int64, string, string) error
	PermissionReleaseUsers(context.Context, int64, int64) ([]int64, error)
}
