package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
)

func (m *notificationManager) TemplateVariables(ctx context.Context, a channel.Actor) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	codes := []string{}
	if err := q.Table("notify_template").Where("tenant_id=? OR tenant_id IS NULL", a.TenantID).
		Distinct("code").Order("code").Pluck("code", &codes).Error; err != nil {
		return nil, notificationDBError(err)
	}
	out := map[string][]channel.Variable{}
	for _, code := range codes {
		out[code] = channel.Variables(code)
	}
	return out, nil
}

func (m *notificationManager) templateByID(ctx context.Context, tenant, id int64) (channel.Template, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	var row channel.Template
	err := q.Table("notify_template").Select(templateColumns).
		Where("id=? AND (tenant_id=? OR tenant_id IS NULL)", id, tenant).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, errs.New(10003, 404, "模板不存在")
	}
	return row, notificationDBError(err)
}

func (m *notificationManager) PreviewTemplate(ctx context.Context, a channel.Actor, id int64) (any, error) {
	row, err := m.templateByID(ctx, a.TenantID, id)
	if err != nil {
		return nil, err
	}
	row.Enabled = true
	msg, err := channel.Render(row, channel.ExampleVariables(row.Code))
	if err != nil {
		return nil, errs.New(10001, 400, "模板无法渲染，请检查变量和格式")
	}
	return channel.TemplatePreview{Subject: msg.Subject, Body: msg.Body}, nil
}

func (m *notificationManager) TestTemplate(ctx context.Context, a channel.Actor, id int64) (any, error) {
	row, err := m.templateByID(ctx, a.TenantID, id)
	if err != nil {
		return nil, err
	}
	if !row.Enabled {
		return nil, errs.New(10004, 409, "模板已停用")
	}
	if _, err := m.PreviewTemplate(ctx, a, id); err != nil {
		return nil, err
	}
	providers, err := m.store.Providers(ctx, a.TenantID, "email")
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, errs.New(10004, 409, "没有可用的邮件通道")
	}
	d, err := m.delivery.Enqueue(ctx, a.TenantID, channel.Request{Channel: "email", TemplateCode: row.Code,
		TemplateID: row.ID, Locale: row.Locale, UserID: a.UserID, ProviderID: providers[0].ID, Test: true,
		Variables: channel.ExampleVariables(row.Code)})
	if err != nil {
		return nil, err
	}
	return d, m.record(ctx, a, "notification.template.test", id, nil, map[string]any{"delivery_id": d.ID})
}

func (m *notificationManager) messageTemplate(ctx context.Context, tenant int64, in channel.Request) (channel.Template, error) {
	if in.Test && in.TemplateID > 0 {
		return m.templateByID(ctx, tenant, in.TemplateID)
	}
	return m.store.Template(ctx, tenant, in.TemplateCode, in.Locale)
}

func (m *notificationManager) testRecipient(ctx context.Context, tenant int64, in channel.Request) (string, error) {
	p, err := m.provider(ctx, channel.Actor{TenantID: tenant}, in.ProviderID, false)
	if err != nil {
		return "", err
	}
	var cfg channel.Config
	if err := channel.OpenJSON(m.sealer, channel.ProviderAAD(p), p.Config, &cfg); err != nil {
		return "", errs.New(10002, 500, "通知配置不可用")
	}
	defer clear(cfg)
	if cfg["test_recipient"] != "" {
		return cfg["test_recipient"], nil
	}
	if in.TemplateID > 0 && in.Channel == "email" {
		return cfg["from_address"], nil
	}
	return m.store.Recipient(ctx, tenant, in.UserID, in.Channel)
}
