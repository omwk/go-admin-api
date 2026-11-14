package setting

func bounds(n float64) *float64 { return &n }

// DefaultRegistry returns the registered schemas and their code defaults.
func DefaultRegistry() *Registry {
	return &Registry{items: []Item{
		{Group: "tenant", Key: "allow_self_registration", Type: "boolean", Default: false, Desc: "允许邮箱验证后自助注册", HotReload: true},
		{Group: "tenant", Key: "default_role_code", Type: "string", Default: "", Max: bounds(64), Desc: "非首用户的默认角色，空值为无角色", HotReload: true},
		{Group: "tenant", Key: "registration_hourly_limit", Type: "number", Default: 10, Min: bounds(1), Max: bounds(10000), Desc: "每租户每小时注册请求上限", HotReload: true},
		{Group: "security", Key: "password_min_length", Type: "number", Default: 8, Min: bounds(8), Max: bounds(64), Desc: "密码最小字符数", HotReload: true},
		{Group: "security", Key: "password_require_mixed", Type: "boolean", Default: true, Desc: "密码包含字母和数字", HotReload: true},
		{Group: "security", Key: "password_expire_days", Type: "number", Default: 90, Min: bounds(0), Max: bounds(365), Desc: "密码有效天数，0 为不过期", HotReload: true},
		{Group: "security", Key: "password_history_size", Type: "number", Default: 5, Min: bounds(0), Max: bounds(20), Desc: "禁止复用的历史密码数", HotReload: true},
		{Group: "security", Key: "login_max_failures", Type: "number", Default: 5, Min: bounds(3), Max: bounds(20), Desc: "登录失败锁定阈值", HotReload: true},
		{Group: "security", Key: "login_lock_minutes", Type: "number", Default: 15, Min: bounds(1), Max: bounds(1440), Desc: "登录锁定分钟数", HotReload: true},
		{Group: "system", Key: "system_name", Type: "string", Default: "CommerceHub", Min: bounds(1), Max: bounds(64), Desc: "系统名称"},
		{Group: "system", Key: "default_page_size", Type: "number", Default: 20, Min: bounds(10), Max: bounds(100), Desc: "默认分页大小", HotReload: true},
		{Group: "system", Key: "timezone", Type: "string", Default: "Asia/Shanghai", Desc: "IANA 时区名称"},
		{Group: "notification", Key: "default_channel", Type: "string", Default: "email", Enum: []string{"email", "sms", "wecom", "dingtalk", "webhook"}, Desc: "默认通知渠道"},
		{Group: "notification", Key: "retry_max_attempts", Type: "number", Default: 3, Min: bounds(0), Max: bounds(10), Desc: "通知最大重试次数"},
		{Group: "sync", Key: "unknown_value_policy", Type: "string", Default: "keep_raw", Enum: []string{"reject", "keep_raw", "fallback_null"}, Desc: "未知枚举值处理策略"},
		{Group: "sync", Key: "drift_alert_threshold", Type: "number", Default: 1, Min: bounds(1), Max: bounds(10000), Desc: "连续漂移告警阈值"},
		{Group: "sync", Key: "auto_promote_baseline", Type: "boolean", Default: false, Desc: "允许自动提升基线"},
	}}
}
