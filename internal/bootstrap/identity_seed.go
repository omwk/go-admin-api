package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type seedStore struct{ *authStore }

const identitySeedTenantID int64 = 1

var _ repo.Seed = (*seedStore)(nil)

// SeedIdentity initializes the configured default tenant without exposing an HTTP seed route.
func SeedIdentity(ctx context.Context, database *db.DB, password string) error {
	service, err := identity.NewSeedService(&seedStore{authStore: newAuthStore(database)})
	if err != nil {
		return err
	}
	if err := service.Run(ctx, identitySeedTenantID, password); err != nil {
		return err
	}
	return seedSystemDictionaries(ctx, database, identitySeedTenantID)
}

func (s *seedStore) LockSeed(ctx context.Context, tenant int64) error {
	if tenant <= 0 {
		return errors.New("seed tenant is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0)), pg_advisory_xact_lock(hashtextextended(?, 0))",
		fmt.Sprintf("identity:seed:%d", tenant), fmt.Sprintf("identity:management:%d", tenant)).Error
}

func (s *seedStore) SeedPermissions(ctx context.Context, tenant int64, rows []model.Permission) error {
	if tenant <= 0 {
		return errors.New("seed tenant is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	for i := range rows {
		if err := permission.Validate(rows[i].Module, rows[i].Code); err != nil {
			return err
		}
		if err := permission.Registered(rows[i].Code); err != nil {
			return err
		}
		rows[i].TenantID = tenant
	}
	return q.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "code"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "module", "type"})}).CreateInBatches(rows, 500).Error
}

func (s *seedStore) SeedMenus(ctx context.Context, tenant int64, rows []repo.SeedMenu) error {
	if tenant <= 0 {
		return errors.New("seed tenant is required")
	}
	parents := map[string]int64{}
	for _, item := range rows {
		row := item.Row
		row.TenantID = tenant
		if item.ParentPath != "" {
			id, exists := parents[item.ParentPath]
			if !exists {
				return errors.New("seed menu parent must precede child")
			}
			row.ParentID = id
		}
		if err := s.ensureMenu(ctx, &row); err != nil {
			return err
		}
		parents[*row.Path] = row.ID
	}
	return nil
}

func (s *seedStore) ensureMenu(ctx context.Context, row *model.Menu) error {
	if row.Path == nil || *row.Path == "" {
		return errors.New("seed menu path is required")
	}
	q, cancel := s.query(ctx)
	defer cancel()
	var existing model.Menu
	err := q.Select("id").Where("tenant_id = ? AND type = ? AND path = ?", row.TenantID, row.Type, *row.Path).Take(&existing).Error
	if err == nil {
		row.ID = existing.ID
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var graph []model.Menu
	if err := q.Select(adminMenuColumns).Where("tenant_id = ?", row.TenantID).Find(&graph).Error; err != nil {
		return err
	}
	if err := identity.ValidateMenuMove(graph, 0, row.ParentID); err != nil {
		return err
	}
	return q.Create(row).Error
}
