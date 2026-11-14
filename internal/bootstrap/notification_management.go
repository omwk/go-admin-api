package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/omwk/go-admin-api/internal/kernel/audit"
	"github.com/omwk/go-admin-api/internal/kernel/crypto"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type notificationManager struct {
	store    *notificationStore
	sealer   crypto.Sealer
	now      func() time.Time
	delivery *channel.Service
}

func newNotificationManager(store *notificationStore, sealer crypto.Sealer, now func() time.Time) *notificationManager {
	m := &notificationManager{store: store, sealer: sealer, now: now, delivery: channel.NewService(store, sealer, now)}
	m.delivery.Prepare = m.prepareAccountMessage
	m.delivery.Recipient = m.accountMessageRecipient
	m.delivery.Allow = func(ctx context.Context, tenant int64, r channel.Request) (bool, error) {
		return notificationPreferenceAllowed(ctx, store.database, tenant, r.UserID, r.TemplateCode, r.Channel)
	}
	return m
}

func (m *notificationManager) provider(ctx context.Context, a channel.Actor, id int64, write bool) (channel.Provider, error) {
	var row channel.Provider
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	if write {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := q.Table("channel_provider").Select(providerColumns).Where("id=? AND (tenant_id=? OR tenant_id IS NULL)", id, a.TenantID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, errs.New(10003, 404, "通道不存在")
	}
	if err != nil {
		return row, notificationDBError(err)
	}
	if write && row.TenantID == nil && !a.Super {
		return row, errs.New(20107, 403, "无权修改全局通道")
	}
	return row, nil
}

func (m *notificationManager) view(row channel.Provider) (channel.ProviderView, error) {
	var cfg channel.Config
	if channel.OpenJSON(m.sealer, channel.ProviderAAD(row), row.Config, &cfg) != nil {
		return channel.ProviderView{}, errs.New(10002, 500, "通知配置不可用")
	}
	defer clear(cfg)
	return channel.ProviderView{Provider: row, Config: channel.SafeConfig(cfg)}, nil
}

func (m *notificationManager) ListProviders(ctx context.Context, a channel.Actor, page, size int) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	query := q.Table("channel_provider").Where("tenant_id=? OR tenant_id IS NULL", a.TenantID)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, notificationDBError(err)
	}
	rows := []channel.Provider{}
	if err := query.Select(providerColumns).Order("priority,id").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, notificationDBError(err)
	}
	views := make([]channel.ProviderView, 0, len(rows))
	for _, row := range rows {
		v, err := m.view(row)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return channel.Page{List: views, Total: total, Page: page, PageSize: size}, nil
}

func (m *notificationManager) SaveProvider(ctx context.Context, a channel.Actor, id int64, in channel.ProviderInput) (any, error) {
	var output channel.ProviderView
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 100 || len(in.Config) > 24 || in.Global && !a.Super {
		return nil, errs.New(10001, 400, "通道配置无效")
	}
	err := m.store.Transaction(ctx, func(tx context.Context) error {
		q, cancel := m.store.database.WithContext(tx)
		defer cancel()
		row := channel.Provider{TenantID: &a.TenantID, Channel: in.Channel, Kind: in.Kind, Name: in.Name, Enabled: in.Enabled, Priority: in.Priority, CreatedAt: m.now().UTC(), UpdatedAt: m.now().UTC(), Config: json.RawMessage(`{}`)}
		var err error
		if in.Global {
			row.TenantID = nil
		}
		if id > 0 {
			row, err = m.provider(tx, a, id, true)
			if err != nil {
				return err
			}
			if row.Channel != in.Channel || row.Kind != in.Kind {
				return errs.New(10001, 400, "通道类型不可更改")
			}
			row.Name, row.Enabled, row.Priority, row.UpdatedAt = in.Name, in.Enabled, in.Priority, m.now().UTC()
			var old channel.Config
			if channel.OpenJSON(m.sealer, channel.ProviderAAD(row), row.Config, &old) != nil {
				return errs.New(10002, 500, "通知配置不可用")
			}
			defer clear(old)
			for k, v := range in.Config {
				if v != "" {
					old[k] = v
				}
			}
			in.Config = old
		}
		if err := channel.ValidateConfig(in.Channel, in.Kind, in.Config); err != nil {
			return errs.New(10001, 400, "通道配置无效")
		}
		delete(in.Config, "test_recipient")
		if id == 0 {
			if err := q.Table("channel_provider").Create(&row).Error; err != nil {
				return providerWriteError(err)
			}
		}
		row.Config, err = channel.SealJSON(m.sealer, channel.ProviderAAD(row), in.Config)
		if err != nil {
			return errs.New(10002, 500, "通知加密不可用")
		}
		result := q.Table("channel_provider").Where("id=?", row.ID).Updates(map[string]any{"name": row.Name, "config": row.Config, "enabled": row.Enabled, "priority": row.Priority, "updated_at": row.UpdatedAt})
		if result.Error != nil {
			return providerWriteError(result.Error)
		}
		if result.RowsAffected != 1 {
			return errs.New(10003, 404, "通道不存在")
		}
		output, err = m.view(row)
		if err != nil {
			return err
		}
		return m.record(tx, a, "notification.provider.save", row.ID, nil, output)
	})
	return output, err
}

func providerWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return errs.New(10004, 409, "通道名称已存在")
	}
	return notificationDBError(err)
}

func (m *notificationManager) DeleteProvider(ctx context.Context, a channel.Actor, id int64) error {
	return m.store.Transaction(ctx, func(tx context.Context) error {
		row, err := m.provider(tx, a, id, true)
		if err != nil {
			return err
		}
		q, cancel := m.store.database.WithContext(tx)
		defer cancel()
		var references int64
		if err := q.Table("notify_delivery").Where("provider_id=?", row.ID).Count(&references).Error; err != nil {
			return err
		}
		if references > 0 {
			return errs.New(10004, 409, "通道已有投递记录，请停用通道")
		}
		result := q.Table("channel_provider").Where("id=?", row.ID).Delete(&channel.Provider{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errs.New(10003, 404, "通道不存在")
		}
		// Retain the entire encrypted snapshot; never put plaintext config into audit.
		before := map[string]any{"id": row.ID, "tenant_id": row.TenantID, "channel": row.Channel, "kind": row.Kind, "name": row.Name, "config": row.Config, "enabled": row.Enabled, "priority": row.Priority, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
		return m.record(tx, a, "notification.provider.delete", row.ID, before, nil)
	})
}

func (m *notificationManager) record(ctx context.Context, a channel.Actor, action string, id int64, before, after any) error {
	beforeMap, err := notificationAuditMap(before)
	if err != nil {
		return err
	}
	afterMap, err := notificationAuditMap(after)
	if err != nil {
		return err
	}
	return audit.Record(ctx, m.store.database, audit.Event{TenantID: a.TenantID, ActorType: "user", ActorID: &a.UserID, Action: action, ResourceType: "notification", ResourceID: fmtNotificationID(id), Result: "success", Before: beforeMap, After: afterMap, AfterNull: before != nil && after == nil})
}

func notificationAuditMap(value any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (m *notificationManager) ListTemplates(ctx context.Context, a channel.Actor, page, size int) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	query := q.Table("notify_template").Where("tenant_id=? OR tenant_id IS NULL", a.TenantID)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, notificationDBError(err)
	}
	rows := []channel.Template{}
	err := query.Select(templateColumns).Order("code,locale,tenant_id NULLS LAST").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	return channel.Page{List: rows, Total: total, Page: page, PageSize: size}, notificationDBError(err)
}

func (m *notificationManager) PutTemplate(ctx context.Context, a channel.Actor, code string, in channel.Template) (any, error) {
	if len(code) == 0 || len(code) > 100 || len(in.Locale) > 30 || len(in.Subject) > 500 || len(in.Body) > 128*1024 {
		return nil, errs.New(10001, 400, "模板无效")
	}
	if in.Locale == "" {
		in.Locale = "zh-CN"
	}
	check := in
	check.Enabled = true
	if _, err := channel.Render(check, map[string]string{"Name": "", "Platform": "", "Severity": "", "Path": ""}); err != nil {
		return nil, errs.New(10001, 400, "模板只允许白名单变量")
	}
	in.ID, in.Code, in.TenantID = 0, code, &a.TenantID
	err := m.store.Transaction(ctx, func(tx context.Context) error {
		q, cancel := m.store.database.WithContext(tx)
		defer cancel()
		err := q.Table("notify_template").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "code"}, {Name: "locale"}}, TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tenant_id IS NOT NULL"}}}, DoUpdates: clause.AssignmentColumns([]string{"subject", "body", "format", "enabled"})}).Create(&in).Error
		if err != nil {
			return err
		}
		return m.record(tx, a, "notification.template.save", in.ID, nil, map[string]any{"code": code, "locale": in.Locale, "enabled": in.Enabled})
	})
	return in, err
}
