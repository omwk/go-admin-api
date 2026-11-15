package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/system"
)

// DictSeedResult records one committed tenant's dictionary and item counts.
type DictSeedResult struct {
	TenantID                 int64
	BeforeDicts, BeforeItems int64
	AfterDicts, AfterItems   int64
}

// BackfillDictionaries only inserts missing system dictionaries and values for all tenants.
func BackfillDictionaries(ctx context.Context, database *db.DB) ([]DictSeedResult, error) {
	return maintainDictionaries(ctx, database, false)
}

// ResetDictionaries replaces all tenant dictionaries with the code-defined seeds.
// The development CLI must reject production configuration before calling it.
func ResetDictionaries(ctx context.Context, database *db.DB) ([]DictSeedResult, error) {
	return maintainDictionaries(ctx, database, true)
}

func maintainDictionaries(ctx context.Context, database *db.DB, reset bool) ([]DictSeedResult, error) {
	q, cancel := database.WithContext(ctx)
	var tenants []int64
	err := q.Table("tenant").Order("id").Pluck("id", &tenants).Error
	cancel()
	if err != nil {
		return nil, err
	}
	results := make([]DictSeedResult, 0, len(tenants))
	for _, tenant := range tenants {
		result, err := maintainTenantDictionaries(ctx, database, tenant, reset)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func maintainTenantDictionaries(ctx context.Context, database *db.DB, tenant int64, reset bool) (DictSeedResult, error) {
	result := DictSeedResult{TenantID: tenant}
	store := newSystemDictStore(database)
	err := database.Transaction(ctx, func(tx context.Context) error {
		if err := store.LockDicts(tx, tenant); err != nil {
			return err
		}
		if err := countTenantDictionaries(tx, database, tenant, &result.BeforeDicts, &result.BeforeItems); err != nil {
			return err
		}
		if reset {
			q, cancel := database.WithContext(tx)
			defer cancel()
			if err := q.Where("tenant_id=?", tenant).Delete(&system.DictItem{}).Error; err != nil {
				return err
			}
			if err := q.Where("tenant_id=?", tenant).Delete(&system.Dict{}).Error; err != nil {
				return err
			}
		}
		if err := store.seedDictsForTenant(tx, tenant); err != nil {
			return err
		}
		return countTenantDictionaries(tx, database, tenant, &result.AfterDicts, &result.AfterItems)
	})
	return result, err
}

func countTenantDictionaries(ctx context.Context, database *db.DB, tenant int64, dicts, items *int64) error {
	q, cancel := database.WithContext(ctx)
	defer cancel()
	if err := q.Model(&system.Dict{}).Where("tenant_id=?", tenant).Count(dicts).Error; err != nil {
		return err
	}
	return q.Model(&system.DictItem{}).Where("tenant_id=?", tenant).Count(items).Error
}
