package channel

import "strings"

// Variable declares a public template input without exposing delivery credentials.
type Variable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Example     string `json:"example"`
	Required    bool   `json:"required"`
}

// Transactional identifies security messages that cannot be opted out of.
func Transactional(code string) bool {
	return code == "identity.verify" || code == "identity.reset" || code == "identity.invite" || code == "identity.email_change" || code == "identity.email_change_notice"
}

// Variables is the single declaration catalog for validation, examples and management.
func Variables(code string) []Variable {
	switch {
	case strings.HasPrefix(code, "identity."):
		unit, lifetime := "小时", "24"
		if code == "identity.reset" {
			unit, lifetime = "分钟", "30"
		} else if code == "identity.invite" {
			lifetime = "168"
		}
		variables := []Variable{
			{"AppName", "系统名称", "CommerceHub", true},
			{"UserName", "用户显示名称", "示例用户", true},
			{"TenantName", "租户名称", "示例租户", true},
			{"Link", "操作链接（预览为无令牌的示例地址）", "https://example.invalid/account", true},
			{"ExpiresInHours", "链接有效期，单位：" + unit, lifetime, true},
			{"ExpiresAt", "到期时间（RFC3339 UTC）", "2026-11-20T00:00:00Z", true},
			{"SupportEmail", "客服邮箱，可为空", "support@example.invalid", false},
		}
		if code == "identity.email_change" || code == "identity.email_change_notice" {
			variables = append(variables, Variable{"NewEmail", "待验证的新邮箱", "new@example.invalid", true},
				Variable{"RequestedAt", "申请时间（RFC3339 UTC）", "2026-11-19T00:00:00Z", true},
				Variable{"IP", "申请来源 IP", "192.0.2.8", false}, Variable{"UserAgent", "客户端摘要，最多 256 字节", "Example Browser", false})
			if code == "identity.email_change_notice" {
				variables[3] = Variable{"Link", "安全提醒不包含验证链接", "", false}
			}
		}
		return variables
	case code == "notification.test":
		return []Variable{}
	case code == "notification.broadcast":
		return []Variable{{"Title", "公告标题", "系统维护通知", true}, {"Body", "公告正文", "系统将进行例行维护。", true}}
	case code == "notification.default":
		return []Variable{{"Name", "用户名称", "示例用户", true}}
	case strings.HasPrefix(code, "credential."):
		return []Variable{{"Platform", "平台名称", "示例平台", true}, {"Severity", "通知级别", "critical", true}, {"Path", "通知中心路径", "/admin/notifications", true}}
	default:
		// Existing custom templates retain their documented legacy substitution set.
		return []Variable{{"Name", "用户名称", "示例用户", true}, {"Platform", "平台名称", "示例平台", true},
			{"Severity", "通知级别", "critical", true}, {"Path", "通知中心路径", "/admin/notifications", true}}
	}
}

// ExampleVariables fills every declared variable, including optional ones.
func ExampleVariables(code string) map[string]string {
	values := map[string]string{}
	for _, variable := range Variables(code) {
		values[variable.Name] = variable.Example
	}
	return values
}

// ValidateTemplate validates both parts using only the code's declared variables.
func ValidateTemplate(t Template) error {
	variables := ExampleVariables(t.Code)
	if _, err := renderPart(t.Subject, variables, false); err != nil {
		return err
	}
	_, err := renderPart(t.Body, variables, t.Format == "html")
	return err
}
