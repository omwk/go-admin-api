package setting

// DefaultSecurity derives fallback policy from the same registry exposed by the API.
func DefaultSecurity() Security {
	values := map[string]any{}
	items, _ := DefaultRegistry().Items("security")
	for _, item := range items {
		values[item.Key] = item.Default
	}
	return Security{PasswordMinLength: values["password_min_length"].(int), PasswordRequireMixed: values["password_require_mixed"].(bool), PasswordExpireDays: values["password_expire_days"].(int), PasswordHistorySize: values["password_history_size"].(int), LoginMaxFailures: values["login_max_failures"].(int), LoginLockMinutes: values["login_lock_minutes"].(int)}
}
