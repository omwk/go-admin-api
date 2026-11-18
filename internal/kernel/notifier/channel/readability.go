package channel

import (
	"net/mail"
	"strings"
)

// DeliveryView adds display metadata without persisting template titles or messages.
type DeliveryView struct {
	Delivery
	TemplateName     string `json:"template_name"`
	LastErrorMessage string `json:"last_error_message" gorm:"-"`
}

// DeliveryFilter contains exact, optional delivery history filters.
type DeliveryFilter struct {
	Status       string `form:"status" binding:"omitempty,oneof=pending sent failed skipped"`
	Channel      string `form:"channel" binding:"omitempty,oneof=email sms wecom dingtalk webhook"`
	TemplateCode string `form:"template_code" binding:"max=100"`
}

// RetryBatchResult counts requeued and ineligible tenant deliveries.
type RetryBatchResult struct {
	Retried int `json:"retried"`
	Skipped int `json:"skipped"`
}

// MaskRecipient returns a safe channel-specific label, or empty for invalid input.
func MaskRecipient(channel, recipient string) string {
	switch channel {
	case "email":
		a, err := mail.ParseAddress(recipient)
		if err != nil || a.Address != recipient {
			return ""
		}
		at := strings.LastIndexByte(recipient, '@')
		if at <= 0 {
			return ""
		}
		return string([]rune(recipient[:at])[0]) + "***@" + recipient[at+1:]
	case "sms":
		digits, international := strings.CutPrefix(recipient, "+")
		if len(digits) <= 7 || len(digits) > 15 {
			return ""
		}
		for _, digit := range digits {
			if digit < '0' || digit > '9' {
				return ""
			}
		}
		prefix := ""
		if international {
			prefix = "+"
		}
		return prefix + digits[:3] + "****" + digits[len(digits)-4:]
	}
	return ""
}

// DeliveryErrorMessage translates safe delivery codes while preserving unknown codes.
func DeliveryErrorMessage(code string) string {
	switch code {
	case "message_unavailable":
		return "消息内容已过期或模板已删除"
	case "recipient_unavailable":
		return "收件人无效或已退订"
	case "provider_delivery_failed":
		return "服务商返回失败"
	case "channel_unavailable":
		return "没有可用的发送渠道"
	case "provider_timeout":
		return "服务商响应超时"
	case "config_invalid":
		return "渠道配置无效"
	default:
		return code
	}
}

func (d *Delivery) recordRecipient(recipient string) {
	d.RecipientHash, d.RecipientDomain = recipientFingerprint(recipient)
	d.RecipientMasked = nil
	if masked := MaskRecipient(d.Channel, recipient); masked != "" {
		d.RecipientMasked = &masked
	}
}
