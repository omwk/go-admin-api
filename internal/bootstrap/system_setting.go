package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"github.com/omwk/go-admin-api/internal/modules/system"
)

type systemSettingStore struct{ database *db.DB }

// Values implements the same registry-backed read port for persistent account history limits.
func (s *systemSettingStore) Values(ctx context.Context, tenant int64, group string) (map[string]any, error) {
	items, ok := setting.DefaultRegistry().Items(group)
	if !ok {
		return nil, errs.New(errs.ErrNotFound, 404, "配置分组不存在")
	}
	stored, err := s.ReadSettings(ctx, tenant, group)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, item := range items {
		value := item.Default
		if raw, ok := stored[item.Key]; ok {
			value, err = setting.Decode(item, raw)
			if err != nil {
				return nil, err
			}
		}
		result[item.Key] = value
	}
	return result, nil
}

var _ system.SettingStore = (*systemSettingStore)(nil)

func (s *systemSettingStore) Transaction(ctx context.Context, run func(context.Context) error) error {
	return s.database.Transaction(ctx, run)
}

func (s *systemSettingStore) LockSettings(ctx context.Context, tenant int64, group string) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("system:settings:%d:%s", tenant, group)).Error
}

func (s *systemSettingStore) ReadSettings(ctx context.Context, tenant int64, group string) (map[string]json.RawMessage, error) {
	if group == "tenant" {
		return s.readTenantSettings(ctx, tenant)
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var rows []struct {
		ItemKey string
		Value   json.RawMessage
	}
	err := q.Table("sys_setting").Select("item_key,value").Where("tenant_id=? AND group_key=?", tenant, group).Find(&rows).Error
	result := map[string]json.RawMessage{}
	for _, row := range rows {
		result[row.ItemKey] = row.Value
	}
	return result, err
}

func (s *systemSettingStore) SaveSetting(ctx context.Context, tenant, user int64, group, key string, value json.RawMessage) error {
	if group == "tenant" {
		return s.saveTenantSetting(ctx, tenant, key, value)
	}
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	return q.Exec(`INSERT INTO sys_setting(tenant_id,group_key,item_key,value,updated_by) VALUES(?,?,?,?::jsonb,?)
 ON CONFLICT(tenant_id,group_key,item_key) DO UPDATE SET value=EXCLUDED.value,updated_by=EXCLUDED.updated_by,updated_at=now()`, tenant, group, key, string(value), user).Error
}
