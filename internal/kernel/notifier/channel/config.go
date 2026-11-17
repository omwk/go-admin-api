package channel

import (
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
)

// ConfigError identifies a configuration field and a safe, user-readable reason.
type ConfigError struct {
	Field  string
	Reason string
}

// Error returns the public reason without including configuration values.
func (e *ConfigError) Error() string { return e.Reason }

// ValidateConfig checks transport identity and rejects unsupported or unsafe settings.
func ValidateConfig(channel, kind string, c Config) error {
	if len(c) > 24 {
		return &ConfigError{Field: "config", Reason: "配置项数量不能超过 24 项"}
	}
	for k, v := range c {
		if !configKeys[k] {
			return &ConfigError{Field: "config", Reason: "包含不支持的配置项"}
		}
		if len(v) > 4096 {
			return &ConfigError{Field: k, Reason: "配置值不能超过 4096 字节"}
		}
		if strings.ContainsAny(v, "\r\n") {
			return &ConfigError{Field: k, Reason: "配置值不能包含换行符"}
		}
	}
	allowed := map[string]string{"smtp": "email", "aliyun_dm": "email", "sendgrid": "email", "aliyun_sms": "sms", "wecom_bot": "wecom", "dingtalk_bot": "dingtalk", "http": "webhook"}
	if allowed[kind] == "" {
		return &ConfigError{Field: "kind", Reason: "不支持的通知通道类型"}
	}
	if allowed[kind] != channel || channel == "" {
		return &ConfigError{Field: "channel", Reason: "通知通道与类型不匹配"}
	}
	if kind == "smtp" {
		return validateSMTP(c)
	}
	if err := validateEndpoint(c["endpoint"]); err != nil {
		return err
	}
	for _, key := range requiredConfig(kind) {
		if c[key] == "" {
			return &ConfigError{Field: key, Reason: "缺少必填配置：" + key}
		}
	}
	if channel == "email" {
		return validateAddress(c["from_address"])
	}
	return nil
}

var configKeys = map[string]bool{
	"smtp_addr": true, "username": true, "password": true, "from_address": true, "from_name": true, "tls_mode": true,
	"endpoint": true, "api_key": true, "access_key_id": true, "access_key_secret": true, "region": true,
	"sign_name": true, "template_code": true, "secret": true, "test_recipient": true,
}

func requiredConfig(kind string) []string {
	switch kind {
	case "sendgrid":
		return []string{"api_key", "from_address"}
	case "aliyun_dm":
		return []string{"access_key_id", "access_key_secret", "region", "from_address"}
	case "aliyun_sms":
		return []string{"access_key_id", "access_key_secret", "region", "sign_name", "template_code"}
	case "http":
		return []string{"secret"}
	}
	return nil
}

func validateSMTP(c Config) error {
	if c["smtp_addr"] == "" {
		return &ConfigError{Field: "smtp_addr", Reason: "缺少必填配置：smtp_addr"}
	}
	host, port, err := net.SplitHostPort(c["smtp_addr"])
	if err != nil {
		return &ConfigError{Field: "smtp_addr", Reason: "SMTP 地址需包含端口，例如 smtp.163.com:465"}
	}
	if host == "" {
		return &ConfigError{Field: "smtp_addr", Reason: "SMTP 地址需包含有效主机和端口"}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return &ConfigError{Field: "smtp_addr", Reason: "SMTP 端口需在 1–65535 之间"}
	}
	if c["tls_mode"] != "" && c["tls_mode"] != "tls" && c["tls_mode"] != "starttls" && c["tls_mode"] != "plain" {
		return &ConfigError{Field: "tls_mode", Reason: "TLS 模式只能是 tls、starttls 或 plain"}
	}
	if c["tls_mode"] == "plain" && !loopback(host) {
		return &ConfigError{Field: "tls_mode", Reason: "明文连接仅允许本机地址，请改用 TLS"}
	}
	if c["password"] != "" && c["username"] == "" {
		return &ConfigError{Field: "username", Reason: "已填写密码时必须同时填写 SMTP 用户名"}
	}
	if c["username"] != "" && c["password"] == "" {
		return &ConfigError{Field: "password", Reason: "已填写用户名时必须同时填写 SMTP 密码"}
	}
	return validateAddress(c["from_address"])
}

func validateAddress(address string) error {
	if address == "" {
		return &ConfigError{Field: "from_address", Reason: "缺少必填配置：from_address"}
	}
	a, err := mail.ParseAddress(address)
	if err != nil || a.Address != address || strings.ContainsAny(address, "\r\n") {
		return &ConfigError{Field: "from_address", Reason: "发件人邮箱格式不正确"}
	}
	return nil
}

func validateEndpoint(raw string) error {
	if raw == "" {
		return &ConfigError{Field: "endpoint", Reason: "缺少必填配置：endpoint"}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return &ConfigError{Field: "endpoint", Reason: "服务地址必须是 http(s) URL"}
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return &ConfigError{Field: "endpoint", Reason: "服务地址必须是 http(s) URL"}
	}
	return nil
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// SafeConfig returns an allowlist projection and presence flags for every sensitive field.
func SafeConfig(c Config) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"tls_mode", "region", "from_name", "sign_name", "template_code"} {
		if c[key] != "" {
			out[key] = c[key]
		}
	}
	for _, key := range []string{"password", "username", "api_key", "access_key_id", "access_key_secret", "secret", "from_address", "smtp_addr", "endpoint"} {
		out[key+"_configured"] = c[key] != ""
	}
	return out
}

// NewSender creates one of the seven first-release transport implementations.
func NewSender(channel, kind string, c Config) (Sender, error) {
	if err := ValidateConfig(channel, kind, c); err != nil {
		return nil, err
	}
	copyConfig := make(Config, len(c))
	for k, v := range c {
		copyConfig[k] = v
	}
	if kind == "smtp" {
		return &smtpSender{config: copyConfig}, nil
	}
	return &httpSender{channel: channel, kind: kind, config: copyConfig}, nil
}
