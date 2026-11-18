package channel

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaskRecipient(t *testing.T) {
	// BE-IDN-015 §3 #2: generate private values instead of literal destinations.
	local := hex.EncodeToString([]byte(t.Name()))
	email := "a" + local + "@163.com"
	phone := "138" + strings.Map(func(r rune) rune { return '0' + r%10 }, local[:4]) + "1234"
	for _, tc := range []struct{ name, channel, recipient, want string }{
		{"email", "email", email, "a***@163.com"},
		{"sms", "sms", phone, "138****1234"},
		{"international", "sms", "+" + phone, "+138****1234"},
		{"unicode", "email", "张" + local + "@example.invalid", "张***@example.invalid"},
		{"empty", "email", "", ""},
		{"invalid_email", "email", local, ""},
		{"display_address", "email", "Name <" + email + ">", ""},
		{"short_phone", "sms", local[:4], ""},
		{"invalid_phone", "sms", local, ""},
		{"other_channel", "webhook", email, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, MaskRecipient(tc.channel, tc.recipient))
		})
	}
}

func TestDeliveryErrorMessage(t *testing.T) {
	// BE-IDN-015 §3 #4/#5: known codes translate; unknown codes stay visible.
	for _, tc := range []struct{ code, want string }{
		{"message_unavailable", "消息内容已过期或模板已删除"},
		{"recipient_unavailable", "收件人无效或已退订"},
		{"provider_delivery_failed", "服务商返回失败"},
		{"channel_unavailable", "没有可用的发送渠道"},
		{"provider_timeout", "服务商响应超时"},
		{"config_invalid", "渠道配置无效"},
		{"future_error", "future_error"},
		{"", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			require.Equal(t, tc.want, DeliveryErrorMessage(tc.code))
		})
	}
}
