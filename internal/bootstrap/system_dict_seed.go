package bootstrap

import (
	"context"
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/modules/system"
	"gorm.io/gorm/clause"
)

func seedSystemDictionaries(ctx context.Context, database *db.DB, tenant int64) error {
	store := newSystemDictStore(database)
	return database.Transaction(ctx, func(tx context.Context) error {
		if err := store.LockDicts(tx, tenant); err != nil {
			return err
		}
		q, cancel := database.WithContext(tx)
		defer cancel()
		for _, seed := range []struct {
			kind, name     string
			values, labels []string
		}{
			{"user_status", "用户状态", []string{"active", "disabled", "locked"}, []string{"正常", "停用", "锁定"}},
			{"sync_run_status", "同步运行状态", []string{"pending", "running", "succeeded", "failed"}, []string{"等待中", "运行中", "成功", "失败"}},
		} {
			row := system.Dict{TenantID: tenant, Type: seed.kind, Name: seed.name, Status: "active", Scope: system.DictScopeSystem, Builtin: true}
			if err := q.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "type"}}, DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
			if err := q.Select(systemDictColumns).Where("tenant_id=? AND type=?", tenant, seed.kind).Take(&row).Error; err != nil {
				return err
			}
			if row.Scope != system.DictScopeSystem {
				continue
			}
			for i, value := range seed.values {
				item := system.DictItem{TenantID: tenant, DictID: row.ID, Label: seed.labels[i], Value: value, Sort: i + 1, Status: "active", Builtin: true, Extra: json.RawMessage("{}")}
				if err := q.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "dict_id"}, {Name: "value"}}, DoNothing: true}).Create(&item).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
