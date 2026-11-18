package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm/clause"
)

func seededNotificationTemplates() []channel.Template {
	rows := []channel.Template{
		{Code: "notification.test", Subject: "通道测试", Body: "通知通道测试"},
		{Code: "notification.default", Subject: "系统通知", Body: "{{.Name}}，您有一条系统通知。"},
		{Code: "identity.verify", Subject: "验证你的 {{.AppName}} 邮箱", Body: `{{.UserName}} 你好：

你正在 {{.AppName}}（租户：{{.TenantName}}）注册账号。请打开下面的链接验证邮箱：

{{.Link}}

该链接 {{.ExpiresInHours}} 小时内有效，且只能使用一次。
如果这不是你本人的操作，可以忽略本邮件，你的邮箱不会被注册。

—— {{.AppName}}
{{if .SupportEmail}}如遇问题请联系：{{.SupportEmail}}{{end}}`},
		{Code: "identity.reset", Subject: "重置你的 {{.AppName}} 密码", Body: `{{.UserName}} 你好：

我们收到了重置 {{.AppName}}（租户：{{.TenantName}}）账号密码的请求。
请打开下面的链接设置新密码：

{{.Link}}

该链接 {{.ExpiresInHours}} 分钟内有效，且只能使用一次。
如果这不是你本人的操作，请忽略本邮件，并建议联系管理员检查账号安全。

—— {{.AppName}}
{{if .SupportEmail}}如遇问题请联系：{{.SupportEmail}}{{end}}`},
		{Code: "identity.invite", Subject: "{{.AppName}} 邀请你加入 {{.TenantName}}", Body: `你好：

管理员邀请你加入 {{.AppName}} 的租户「{{.TenantName}}」。
请打开下面的链接接受邀请并设置登录密码：

{{.Link}}

该链接 {{.ExpiresInHours}} 小时内有效，且只能使用一次。
接受邀请后，你可以用邮箱和刚设置的密码登录。

—— {{.AppName}}
{{if .SupportEmail}}如遇问题请联系：{{.SupportEmail}}{{end}}`},
	}
	for _, code := range []string{"credential.expiring.3d", "credential.expiring.1d", "credential.expired", "credential.needs_reauth", "credential.unknown", "credential.failed", "credential.stuck", "credential.recovered", "credential.protocol_pending"} {
		rows = append(rows, channel.Template{Code: code, Subject: "凭据维护通知", Body: "{{.Platform}}: {{.Severity}} — {{.Path}}"})
	}
	for i := range rows {
		rows[i].Locale, rows[i].Format, rows[i].Enabled = "zh-CN", "text", true
	}
	return rows
}

func seedNotificationTemplates(ctx context.Context, database *db.DB) error {
	return database.Transaction(ctx, func(tx context.Context) error {
		q, cancel := database.WithContext(tx)
		defer cancel()
		for _, row := range seededNotificationTemplates() {
			if err := q.Table("notify_template").Clauses(clause.OnConflict{
				Columns:     []clause.Column{{Name: "code"}, {Name: "locale"}},
				TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tenant_id IS NULL"}}},
				DoUpdates:   clause.AssignmentColumns([]string{"subject", "body", "format"}),
			}).Create(&row).Error; err != nil {
				return err
			}
			if err := q.Table("notify_template").Where("tenant_id IS NOT NULL AND code=? AND locale=?", row.Code, row.Locale).
				Updates(map[string]any{"subject": row.Subject, "body": row.Body, "format": row.Format}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
