package config

// Tenant identifies the tenant whose super users administer the global directory.
type Tenant struct {
	PlatformID  int64  `mapstructure:"platform_id"`
	DefaultCode string `mapstructure:"default_code"`
}
