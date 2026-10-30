package identity

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/pwd"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

// Stable names used only by the minimum initialization catalog.
const (
	SuperAdminRoleCode = "super_admin"
	OperatorRoleCode   = "operator"
	SalesRoleCode      = "sales"
	SeedAdminUsername  = "admin"
	SeedDepartmentName = "根部门"
)

// SeedService owns the initialization transaction and dependency order.
type SeedService struct{ store repo.Seed }

// NewSeedService accepts the dedicated bootstrap seed adapter.
func NewSeedService(store repo.Seed) (*SeedService, error) {
	if store == nil {
		return nil, errors.New("seed store is required")
	}
	return &SeedService{store: store}, nil
}

// Run hashes the environment password outside the transaction, preserving existing accounts.
func (s *SeedService) Run(ctx context.Context, tenant int64, password string) error {
	if tenant <= 0 || password == "" {
		return errors.New("seed tenant and CH_SEED_ADMIN_PASSWORD are required")
	}
	hash, err := pwd.Hash(password)
	if err != nil {
		return err
	}
	permissions := []model.Permission{}
	for _, item := range permission.All() {
		if err := permission.Validate(item.Module, item.Code); err != nil {
			return err
		}
		permissions = append(permissions, model.Permission{Code: item.Code, Name: item.Name, Module: item.Module, Type: item.Type})
	}
	return s.store.Transaction(ctx, func(ctx context.Context) error {
		if err := s.store.LockSeed(ctx, tenant); err != nil {
			return err
		}
		if err := s.store.SeedPermissions(ctx, tenant, permissions); err != nil {
			return err
		}
		if err := s.store.SeedMenus(ctx, tenant, seedMenus()); err != nil {
			return err
		}
		if err := s.store.SeedRoles(ctx, tenant, seedRoles()); err != nil {
			return err
		}
		return s.store.SeedAdmin(ctx, tenant, repo.AdminSeed{Username: SeedAdminUsername, DepartmentName: SeedDepartmentName, RoleCode: SuperAdminRoleCode, PasswordHash: hash})
	})
}
