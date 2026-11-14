package bootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"gorm.io/gorm"
)

func (s *systemSettingStore) readTenantSettings(ctx context.Context, tenant int64) (map[string]json.RawMessage, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct{ Settings json.RawMessage }
	if err := q.Table("tenant").Select("settings").Where("id=?", tenant).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.New(errs.ErrNotFound, 404, "租户不存在")
		}
		return nil, err
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(row.Settings, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (s *systemSettingStore) saveTenantSetting(ctx context.Context, tenant int64, key string, value json.RawMessage) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if key == "default_role_code" {
		var code string
		if err := json.Unmarshal(value, &code); err != nil {
			return err
		}
		if code != "" {
			var count int64
			if err := q.Table("sys_role").Where("tenant_id=? AND code=? AND status='active'", tenant, code).Count(&count).Error; err != nil {
				return err
			}
			if count != 1 {
				return setting.Invalid("tenant", key, "默认角色不存在或已停用")
			}
		}
	}
	result := q.Exec("UPDATE tenant SET settings=jsonb_set(settings,ARRAY[?]::text[],?::jsonb,true),updated_at=now() WHERE id=?", key, string(value), tenant)
	return adminMutation(result)
}
