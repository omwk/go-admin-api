package setting

import "context"

// TenantRegistration is the live, default-closed tenant registration policy.
type TenantRegistration struct {
	Allowed         bool
	DefaultRoleCode string
	HourlyLimit     int
}

// LoadTenantRegistration uses the same registered defaults and validation as settings writes.
func LoadTenantRegistration(ctx context.Context, reader Reader, tenant int64) (TenantRegistration, error) {
	v, err := values(ctx, reader, tenant, "tenant")
	if err != nil {
		return TenantRegistration{}, err
	}
	return TenantRegistration{Allowed: v["allow_self_registration"].(bool), DefaultRoleCode: v["default_role_code"].(string),
		HourlyLimit: int(v["registration_hourly_limit"].(float64))}, nil
}
