package bootstrap

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/setting"
	"gorm.io/gorm"
)

func (s *accountStore) historySize(ctx context.Context, tenant int64) (int, error) {
	items, _ := setting.DefaultRegistry().Items("security")
	for _, item := range items {
		if item.Key != "password_history_size" {
			continue
		}
		q, cancel := s.query(ctx)
		defer cancel()
		var row struct{ Value json.RawMessage }
		err := q.Table("sys_setting").Select("value").Where("tenant_id=? AND group_key=? AND item_key=?", tenant, item.Group, item.Key).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return setting.DefaultSecurity().PasswordHistorySize, nil
		}
		if err != nil {
			return 0, err
		}
		value, err := setting.Decode(item, row.Value)
		if err != nil {
			return 0, err
		}
		return int(value.(float64)), nil
	}
	return 0, errors.New("password history setting is unregistered")
}
