package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func fmtNotificationID(id int64) string { return strconv.FormatInt(id, 10) }

func (m *notificationManager) Write(ctx context.Context, a channel.Actor, op *idem.Operation, status int, deliver bool, fn func(context.Context) (any, error)) (idem.Result, error) {
	var result idem.Result
	store := newIdempotencyStore(m.store.database)
	err := m.store.Transaction(ctx, func(tx context.Context) error {
		if op != nil {
			claim, err := store.Claim(tx, *op)
			if err != nil {
				return notificationIdemError(err)
			}
			if claim.Action == idem.Replay {
				result = claim.Result
				return nil
			}
		}
		value, err := fn(tx)
		if err != nil {
			return err
		}
		result, err = idem.JSONResult(status, value)
		if err != nil {
			return err
		}
		if op != nil {
			result, err = store.Complete(tx, *op, result)
		}
		return err
	})
	if err != nil || !deliver {
		return result, err
	}
	var queued channel.Delivery
	if json.Unmarshal(result.Body, &queued) != nil || queued.ID == 0 {
		return result, errs.New(10002, 500, "投递结果无效")
	}
	d, err := m.delivery.Deliver(ctx, a.TenantID, queued.ID)
	if err != nil {
		return result, err
	}
	if d.ID == 0 {
		d, err = m.deliveryByID(ctx, a.TenantID, queued.ID)
		if err != nil {
			return result, err
		}
	}
	return idem.JSONResult(status, d)
}

func notificationIdemError(err error) error {
	if errors.Is(err, idem.ErrMismatch) {
		return errs.New(10006, 409, "幂等请求不一致")
	}
	if errors.Is(err, idem.ErrProcessing) {
		return errs.New(10004, 409, "请求正在处理中")
	}
	return err
}

func (m *notificationManager) TestProvider(ctx context.Context, a channel.Actor, id int64) (any, error) {
	p, err := m.provider(ctx, a, id, false)
	if err != nil {
		return nil, err
	}
	if err := m.testRate(ctx, a.TenantID); err != nil {
		return nil, err
	}
	d, err := m.delivery.Enqueue(ctx, a.TenantID, channel.Request{Channel: p.Channel, TemplateCode: "notification.test", Locale: "zh-CN", UserID: a.UserID, ProviderID: p.ID, Test: true})
	if err != nil {
		return nil, err
	}
	err = m.record(ctx, a, "notification.provider.test", id, nil, map[string]any{"delivery_id": d.ID})
	return d, err
}

func (m *notificationManager) testRate(ctx context.Context, tenant int64) error {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	window := m.now().UTC().Truncate(time.Minute).Format(time.RFC3339)
	var row struct{ Value json.RawMessage }
	err := q.Raw(`INSERT INTO sys_setting(tenant_id,group_key,item_key,value,updated_at)
 VALUES(?,'notification_test_rate','minute',jsonb_build_object('window',?::text,'count',1),?)
 ON CONFLICT(tenant_id,group_key,item_key) DO UPDATE
 SET value=jsonb_build_object('window',?::text,'count',CASE WHEN sys_setting.value->>'window'=? THEN (sys_setting.value->>'count')::int+1 ELSE 1 END),updated_at=excluded.updated_at
 WHERE sys_setting.value->>'window'<>? OR (sys_setting.value->>'count')::int<3
 RETURNING value`, tenant, window, m.now().UTC(), window, window, window).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errs.New(10005, 429, "每租户每分钟最多测试三次")
	}
	return notificationDBError(err)
}

func (m *notificationManager) deliveryByID(ctx context.Context, tenant, id int64) (channel.Delivery, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	var row channel.Delivery
	err := q.Table("notify_delivery").Select(deliveryColumns).Where("tenant_id=? AND id=?", tenant, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, errs.New(10003, 404, "投递不存在")
	}
	return row, notificationDBError(err)
}

func (m *notificationManager) ListDeliveries(ctx context.Context, a channel.Actor, page, size int) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	query := q.Table("notify_delivery").Where("tenant_id=?", a.TenantID)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, notificationDBError(err)
	}
	rows := []channel.Delivery{}
	err := query.Select(deliveryColumns).Order("created_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	return channel.Page{List: rows, Total: total, Page: page, PageSize: size}, notificationDBError(err)
}

func (m *notificationManager) Retry(ctx context.Context, a channel.Actor, id int64) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	var row channel.Delivery
	err := q.Table("notify_delivery").Select(deliveryColumns).Where("tenant_id=? AND id=?", a.TenantID, id).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.New(10003, 404, "投递不存在")
	}
	if err != nil {
		return nil, notificationDBError(err)
	}
	if row.Status != "failed" && row.Status != "skipped" {
		return nil, errs.New(10004, 409, "投递不可重试")
	}
	result := q.Table("notify_outbox").Where("tenant_id=? AND id=? AND status IN ('failed','channel_unavailable')", a.TenantID, id).Update("status", "pending")
	if result.Error != nil {
		return nil, notificationDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, errs.New(10004, 409, "投递已被处理")
	}
	if err := q.Table("notify_delivery").Where("tenant_id=? AND id=?", a.TenantID, id).Updates(map[string]any{"status": "pending", "next_retry_at": nil}).Error; err != nil {
		return nil, notificationDBError(err)
	}
	row.Status, row.NextRetryAt = "pending", nil
	return row, m.record(ctx, a, "notification.delivery.retry", id, nil, map[string]any{"status": "pending"})
}
