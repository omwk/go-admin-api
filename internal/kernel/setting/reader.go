package setting

import (
	"context"
	"encoding/json"
)

// Reader provides resolved, registered tenant values to consumers.
type Reader interface {
	Values(context.Context, int64, string) (map[string]any, error)
}

// Security contains only the password and login options declared live by S2.
type Security struct {
	PasswordMinLength    int
	PasswordRequireMixed bool
	PasswordExpireDays   int
	PasswordHistorySize  int
	LoginMaxFailures     int
	LoginLockMinutes     int
}

func values(ctx context.Context, reader Reader, tenant int64, group string) (map[string]any, error) {
	items, _ := DefaultRegistry().Items(group)
	result := map[string]any{}
	for _, item := range items {
		value, err := Decode(item, mustJSON(item.Default))
		if err != nil {
			return nil, err
		}
		result[item.Key] = value
	}
	if reader == nil {
		return result, nil
	}
	overrides, err := reader.Values(ctx, tenant, group)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if value, ok := overrides[item.Key]; ok {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, Invalid(group, item.Key, "配置值无效")
			}
			decoded, err := Decode(item, encoded)
			if err != nil {
				return nil, err
			}
			result[item.Key] = decoded
		}
	}
	return result, nil
}

// LoadSecurity reads current tenant policy, falling back to registered defaults.
func LoadSecurity(ctx context.Context, reader Reader, tenant int64) (Security, error) {
	v, err := values(ctx, reader, tenant, "security")
	if err != nil {
		return Security{}, err
	}
	return Security{PasswordMinLength: int(v["password_min_length"].(float64)), PasswordRequireMixed: v["password_require_mixed"].(bool),
		PasswordExpireDays: int(v["password_expire_days"].(float64)), PasswordHistorySize: int(v["password_history_size"].(float64)),
		LoginMaxFailures: int(v["login_max_failures"].(float64)), LoginLockMinutes: int(v["login_lock_minutes"].(float64))}, nil
}

// DefaultPageSize resolves the current default without overriding an explicit request limit.
func DefaultPageSize(ctx context.Context, reader Reader, tenant int64) (int, error) {
	v, err := values(ctx, reader, tenant, "system")
	if err != nil {
		return 0, err
	}
	return int(v["default_page_size"].(float64)), nil
}
