package bootstrap

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"gorm.io/gorm/clause"
)

func seedSystemDictionaries(ctx context.Context, database *db.DB, tenant int64) error {
	return newSystemDictStore(database).seedDictsForTenant(ctx, tenant)
}

func (s *systemDictStore) seedDictsForTenant(ctx context.Context, tenantID int64) error {
	return s.database.Transaction(ctx, func(tx context.Context) error {
		if err := s.LockDicts(tx, tenantID); err != nil {
			return err
		}
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		for _, seed := range dictionarySeeds() {
			row := system.Dict{TenantID: tenantID, Type: seed.kind, Name: seed.name, Status: "active", Scope: system.DictScopeSystem, Builtin: true}
			if err := q.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "type"}}, DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
			if err := q.Select(systemDictColumns).Where("tenant_id=? AND type=?", tenantID, seed.kind).Take(&row).Error; err != nil {
				return err
			}
			if row.Scope != system.DictScopeSystem {
				continue
			}
			for _, value := range seed.items {
				item := system.DictItem{TenantID: tenantID, DictID: row.ID, Label: value.label, Value: value.value, Sort: value.sort, Status: "active", Color: value.color, Builtin: true, Extra: json.RawMessage("{}")}
				if err := q.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "dict_id"}, {Name: "value"}}, DoNothing: true}).Create(&item).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
