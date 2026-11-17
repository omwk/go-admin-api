package bootstrap

import (
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"github.com/stretchr/testify/require"
)

// BE-IDN-014 §3: configuration errors identify the input without exposing credentials.
func TestAdminNotificationConfigErrorsIntegration(t *testing.T) {
	f := newNotificationFixtureF3(t)
	smtp := channel.Config{"smtp_addr": "smtp.example.invalid:465", "from_address": f.email}
	for i, tc := range []struct {
		name, channel, kind string
		config              channel.Config
		field, reason       string
	}{
		{"no_port", "email", "smtp", channel.Config{"smtp_addr": "smtp.example.invalid"}, "smtp_addr", "SMTP 地址需包含端口，例如 smtp.163.com:465"},
		{"password_only", "email", "smtp", channel.Config{"password": strings.Repeat("*", 8)}, "username", "已填写密码时必须同时填写 SMTP 用户名"},
		{"username_only", "email", "smtp", channel.Config{"username": f.email}, "password", "已填写用户名时必须同时填写 SMTP 密码"},
		{"port_range", "email", "smtp", channel.Config{"smtp_addr": "smtp.example.invalid:65536"}, "smtp_addr", "SMTP 端口需在 1–65535 之间"},
		{"from_address", "email", "smtp", channel.Config{"from_address": "invalid"}, "from_address", "发件人邮箱格式不正确"},
		{"http_endpoint", "webhook", "http", channel.Config{"endpoint": "invalid", "secret": f.token}, "endpoint", "服务地址必须是 http(s) URL"},
		{"tls_mode", "email", "smtp", channel.Config{"tls_mode": "invalid"}, "tls_mode", "TLS 模式只能是 tls、starttls 或 plain"},
		{"plain_external", "email", "smtp", channel.Config{"tls_mode": "plain"}, "tls_mode", "明文连接仅允许本机地址，请改用 TLS"},
		{"sendgrid", "email", "sendgrid", channel.Config{"endpoint": "https://example.invalid", "from_address": f.email}, "api_key", "缺少必填配置：api_key"},
		{"aliyun_dm", "email", "aliyun_dm", channel.Config{"endpoint": "https://example.invalid"}, "access_key_id", "缺少必填配置：access_key_id"},
		{"aliyun_sms", "sms", "aliyun_sms", channel.Config{"endpoint": "https://example.invalid"}, "access_key_id", "缺少必填配置：access_key_id"},
		{"wecom_bot", "wecom", "wecom_bot", channel.Config{"endpoint": "invalid"}, "endpoint", "服务地址必须是 http(s) URL"},
		{"dingtalk_bot", "dingtalk", "dingtalk_bot", channel.Config{"endpoint": "invalid"}, "endpoint", "服务地址必须是 http(s) URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := maps.Clone(tc.config)
			if tc.kind == "smtp" {
				cfg = maps.Clone(smtp)
				maps.Copy(cfg, tc.config)
			}
			in := channel.ProviderInput{Channel: tc.channel, Kind: tc.kind, Name: tc.name, Enabled: true, Config: cfg}
			w := f.request(t, http.MethodPost, "/providers", "", in)
			if i < 2 {
				raw, err := json.Marshal(in)
				require.NoError(t, err)
				// This request uses a generated mask, never a real SMTP credential.
				t.Logf("POST /api/v1/admin/notifications/providers\nContent-Type: application/json\n\n%s\nHTTP/1.1 %d\n%s", raw, w.Code, w.Body.String())
			}
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assertNotificationConfigError(t, w.Body.Bytes(), tc.field, tc.reason)
			require.NotContains(t, w.Body.String(), f.token)
			require.NotContains(t, w.Body.String(), f.email)
		})
	}
	q, cancel := f.database.WithContext(f.ctx)
	defer cancel()
	var count int64
	require.NoError(t, q.Table("channel_provider").Count(&count).Error)
	require.Zero(t, count)
	f.assertPrivacy(t, f.token)
}

func assertNotificationConfigError(t *testing.T, raw []byte, field, reason string) {
	t.Helper()
	var envelope struct {
		Code    int
		Message string
		Details struct{ Field, Reason string }
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))
	require.Equal(t, 10001, envelope.Code)
	require.Equal(t, reason, envelope.Message)
	require.Equal(t, field, envelope.Details.Field)
	require.Equal(t, reason, envelope.Details.Reason)
	for _, internal := range []string{"invalid SMTP endpoint", "SMTP authentication incomplete", "invalid notification address", "invalid notification endpoint", "notification HTTPS required", "provider configuration incomplete", "unsupported notification provider"} {
		require.NotContains(t, string(raw), internal)
	}
}

// BE-IDN-014 §3.7: all seven kinds still create, merge partial config and preserve status codes.
func TestAdminNotificationConfigValidIntegration(t *testing.T) {
	f := newNotificationFixtureF3(t)
	for _, tc := range []struct{ channel, kind string }{
		{"email", "smtp"}, {"email", "sendgrid"}, {"email", "aliyun_dm"},
		{"sms", "aliyun_sms"}, {"webhook", "http"}, {"wecom", "wecom_bot"}, {"dingtalk", "dingtalk_bot"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			cfg := channel.Config{
				"endpoint": "https://example.invalid", "smtp_addr": "smtp.example.invalid:465", "from_address": f.email,
				"api_key": f.token, "access_key_id": f.token, "access_key_secret": f.token, "secret": f.token,
				"region": "cn-test", "sign_name": "test", "template_code": "test",
			}
			in := channel.ProviderInput{Channel: tc.channel, Kind: tc.kind, Name: tc.kind, Enabled: true, Config: cfg}
			w := f.request(t, http.MethodPost, "/providers", "", in)
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var provider channel.ProviderView
			notificationDataF3(t, w, &provider)
			path := "/providers/" + fmtNotificationID(provider.ID)
			in.Priority, in.Config = 2, nil
			w = f.request(t, http.MethodPut, path, "", in)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var updated channel.ProviderView
			notificationDataF3(t, w, &updated)
			require.Equal(t, provider.Config, updated.Config)
			require.Equal(t, 2, updated.Priority)
			if tc.kind == "smtp" {
				in.Config = channel.Config{"smtp_addr": "smtp.example.invalid"}
				w = f.request(t, http.MethodPut, path, "", in)
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				assertNotificationConfigError(t, w.Body.Bytes(), "smtp_addr", "SMTP 地址需包含端口，例如 smtp.163.com:465")
				in.Config = nil
				w = f.request(t, http.MethodPut, path, "", in)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			}
		})
	}
	f.assertPrivacy(t, f.token)
}
