package channel

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// BE-IDN-014 §2.3: SMTP diagnostics use the approved Chinese messages and missing fields.
func TestProviderConfigSMTPDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, key, value, field, reason string }{
		{"no_port", "smtp_addr", "smtp.example.invalid", "smtp_addr", "SMTP 地址需包含端口，例如 smtp.163.com:465"},
		{"empty_port", "smtp_addr", "smtp.example.invalid:", "smtp_addr", "SMTP 端口需在 1–65535 之间"},
		{"zero_port", "smtp_addr", "smtp.example.invalid:0", "smtp_addr", "SMTP 端口需在 1–65535 之间"},
		{"negative_port", "smtp_addr", "smtp.example.invalid:-1", "smtp_addr", "SMTP 端口需在 1–65535 之间"},
		{"large_port", "smtp_addr", "smtp.example.invalid:65536", "smtp_addr", "SMTP 端口需在 1–65535 之间"},
		{"nonnumeric_port", "smtp_addr", "smtp.example.invalid:port", "smtp_addr", "SMTP 端口需在 1–65535 之间"},
		{"no_host", "smtp_addr", ":465", "smtp_addr", "SMTP 地址需包含有效主机和端口"},
		{"no_address", "smtp_addr", "", "smtp_addr", "缺少必填配置：smtp_addr"},
		{"password_only", "password", randomValue(t), "username", "已填写密码时必须同时填写 SMTP 用户名"},
		{"username_only", "username", randomValue(t), "password", "已填写用户名时必须同时填写 SMTP 密码"},
		{"tls_mode", "tls_mode", "invalid", "tls_mode", "TLS 模式只能是 tls、starttls 或 plain"},
		{"plain_external", "tls_mode", "plain", "tls_mode", "明文连接仅允许本机地址，请改用 TLS"},
		{"invalid_from", "from_address", "invalid", "from_address", "发件人邮箱格式不正确"},
		{"display_name", "from_address", "Name <sender@example.invalid>", "from_address", "发件人邮箱格式不正确"},
		{"no_from", "from_address", "", "from_address", "缺少必填配置：from_address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{"smtp_addr": "smtp.example.invalid:465", "from_address": randomValue(t) + "@example.invalid"}
			cfg[tc.key] = tc.value
			assertConfigError(t, ValidateConfig("email", "smtp", cfg), tc.field, tc.reason)
		})
	}
}

// BE-IDN-014 §2.1: every non-SMTP kind diagnoses each required field independently.
func TestProviderConfigOtherKindsDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		channel, kind string
		required      []string
	}{
		{"email", "sendgrid", []string{"api_key", "from_address"}},
		{"email", "aliyun_dm", []string{"access_key_id", "access_key_secret", "region", "from_address"}},
		{"sms", "aliyun_sms", []string{"access_key_id", "access_key_secret", "region", "sign_name", "template_code"}},
		{"webhook", "http", []string{"secret"}},
		{"wecom", "wecom_bot", nil},
		{"dingtalk", "dingtalk_bot", nil},
	} {
		cfg := validHTTPConfig(t)
		for _, field := range append([]string{"endpoint"}, tc.required...) {
			t.Run(tc.kind+"/missing_"+field, func(t *testing.T) {
				missing := maps.Clone(cfg)
				delete(missing, field)
				assertConfigError(t, ValidateConfig(tc.channel, tc.kind, missing), field, "缺少必填配置："+field)
			})
		}
		for name, endpoint := range map[string]string{
			"relative": "/relative", "scheme": "ftp://example.invalid", "parse": "https://%zz",
			"userinfo": "https://" + randomValue(t) + "@example.invalid", "fragment": "https://example.invalid/#fragment",
			"remote_plain": "http://example.invalid", "host": "https:///path",
		} {
			t.Run(tc.kind+"/endpoint_"+name, func(t *testing.T) {
				invalid := maps.Clone(cfg)
				invalid["endpoint"] = endpoint
				assertConfigError(t, ValidateConfig(tc.channel, tc.kind, invalid), "endpoint", "服务地址必须是 http(s) URL")
			})
		}
		if tc.channel == "email" {
			t.Run(tc.kind+"/from_address", func(t *testing.T) {
				cfg["from_address"] = "invalid"
				assertConfigError(t, ValidateConfig(tc.channel, tc.kind, cfg), "from_address", "发件人邮箱格式不正确")
			})
		}
	}
}

func TestProviderConfigSafetyDiagnostics(t *testing.T) {
	unknown := randomValue(t)
	large := Config{}
	for i := 0; i < 25; i++ {
		large[fmt.Sprintf("key_%d", i)] = unknown
	}
	for _, tc := range []struct {
		name, channel, kind string
		config              Config
		field, reason       string
	}{
		{"unknown_kind", "email", unknown, nil, "kind", "不支持的通知通道类型"},
		{"wrong_channel", "sms", "smtp", nil, "channel", "通知通道与类型不匹配"},
		{"empty_channel", "", "smtp", nil, "channel", "通知通道与类型不匹配"},
		{"unknown_key", "webhook", "http", Config{unknown: unknown}, "config", "包含不支持的配置项"},
		{"too_many", "webhook", "http", large, "config", "配置项数量不能超过 24 项"},
		{"too_long", "webhook", "http", Config{"secret": strings.Repeat(unknown, 4097)}, "secret", "配置值不能超过 4096 字节"},
		{"newline", "webhook", "http", Config{"secret": unknown + "\r\n"}, "secret", "配置值不能包含换行符"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.channel, tc.kind, tc.config)
			assertConfigError(t, err, tc.field, tc.reason)
			require.NotContains(t, err.Error(), unknown)
		})
	}
}

func TestProviderConfigValidConfigurationsPreserved(t *testing.T) {
	for _, cfg := range []Config{
		{"smtp_addr": "smtp.example.invalid:1", "tls_mode": "tls"},
		{"smtp_addr": "smtp.example.invalid:65535", "tls_mode": "starttls"},
		{"smtp_addr": "127.0.0.1:25", "tls_mode": "plain"},
		{"smtp_addr": "[::1]:25", "tls_mode": "plain"},
		{"smtp_addr": "smtp.example.invalid:465", "username": randomValue(t), "password": randomValue(t)},
	} {
		cfg["from_address"] = randomValue(t) + "@example.invalid"
		_, err := NewSender("email", "smtp", cfg)
		require.NoError(t, err)
	}
	for _, tc := range []struct{ channel, kind string }{
		{"email", "sendgrid"}, {"email", "aliyun_dm"}, {"sms", "aliyun_sms"},
		{"webhook", "http"}, {"wecom", "wecom_bot"}, {"dingtalk", "dingtalk_bot"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			for _, endpoint := range []string{"https://example.invalid", "http://127.0.0.1:8080", "http://[::1]:8080"} {
				cfg := validHTTPConfig(t)
				cfg["endpoint"] = endpoint
				_, err := NewSender(tc.channel, tc.kind, cfg)
				require.NoError(t, err)
			}
		})
	}
}

func validHTTPConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		"endpoint": "https://example.invalid", "from_address": randomValue(t) + "@example.invalid",
		"api_key": randomValue(t), "access_key_id": randomValue(t), "access_key_secret": randomValue(t), "secret": randomValue(t),
		"region": "cn-test", "sign_name": "test", "template_code": "test",
	}
}

func assertConfigError(t *testing.T, err error, field, reason string) {
	t.Helper()
	var configError *ConfigError
	require.ErrorAs(t, err, &configError)
	require.Equal(t, field, configError.Field)
	require.Equal(t, reason, configError.Reason)
	require.EqualError(t, err, reason)
}
