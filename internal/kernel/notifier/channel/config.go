package channel

import (
	"errors"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
)

// ValidateConfig checks transport identity and rejects unsupported or unsafe settings.
func ValidateConfig(channel, kind string, c Config) error {
	if len(c) > 24 {
		return errors.New("invalid provider configuration")
	}
	for k, v := range c {
		if len(v) > 4096 || strings.ContainsAny(v, "\r\n") || !configKeys[k] {
			return errors.New("invalid provider configuration")
		}
	}
	allowed := map[string]string{"smtp": "email", "aliyun_dm": "email", "sendgrid": "email", "aliyun_sms": "sms", "wecom_bot": "wecom", "dingtalk_bot": "dingtalk", "http": "webhook"}
	if allowed[kind] != channel || channel == "" {
		return errors.New("unsupported notification provider")
	}
	if kind == "smtp" {
		return validateSMTP(c)
	}
	if err := validateEndpoint(c["endpoint"]); err != nil {
		return err
	}
	for _, key := range requiredConfig(kind) {
		if c[key] == "" {
			return errors.New("provider configuration incomplete")
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
	host, port, err := net.SplitHostPort(c["smtp_addr"])
	if err != nil || host == "" {
		return errors.New("invalid SMTP endpoint")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return errors.New("invalid SMTP endpoint")
	}
	if c["tls_mode"] != "" && c["tls_mode"] != "tls" && c["tls_mode"] != "starttls" && c["tls_mode"] != "plain" {
		return errors.New("invalid SMTP TLS mode")
	}
	if c["tls_mode"] == "plain" && !loopback(host) {
		return errors.New("SMTP TLS required")
	}
	if (c["username"] == "") != (c["password"] == "") {
		return errors.New("SMTP authentication incomplete")
	}
	return validateAddress(c["from_address"])
}

func validateAddress(address string) error {
	a, err := mail.ParseAddress(address)
	if err != nil || a.Address != address || strings.ContainsAny(address, "\r\n") {
		return errors.New("invalid notification address")
	}
	return nil
}

func validateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("invalid notification endpoint")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return errors.New("notification HTTPS required")
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
