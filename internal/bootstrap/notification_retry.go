package bootstrap

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *notificationManager) Retry(ctx context.Context, a channel.Actor, id int64) (any, error) {
	var row channel.Delivery
	err := m.store.Transaction(ctx, func(tx context.Context) error {
		var err error
		row, err = m.retryDelivery(tx, a, id)
		return err
	})
	return row, err
}

func (m *notificationManager) RetryBatch(ctx context.Context, a channel.Actor, ids []int64) (any, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	if len(ids) == 0 || len(ids) > 100 {
		return nil, errs.New(10001, 400, "投递编号需为 1 至 100 个不重复的正整数")
	}
	for i, id := range ids {
		if id <= 0 || i > 0 && id == ids[i-1] {
			return nil, errs.New(10001, 400, "投递编号需为 1 至 100 个不重复的正整数")
		}
	}
	var out channel.RetryBatchResult
	err := m.store.Transaction(ctx, func(tx context.Context) error {
		for _, id := range ids {
			_, err := m.retryDelivery(tx, a, id)
			if err == nil {
				out.Retried++
				continue
			}
			var app *errs.AppError
			if errors.As(err, &app) && (app.HTTPStatus == 404 || app.HTTPStatus == 409) {
				out.Skipped++
				continue
			}
			return err
		}
		return nil
	})
	return out, err
}

func (m *notificationManager) retryDelivery(ctx context.Context, a channel.Actor, id int64) (channel.Delivery, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	var row channel.Delivery
	err := q.Table("notify_delivery").Select(deliveryColumns).Where("tenant_id=? AND id=?", a.TenantID, id).
		Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, errs.New(10003, 404, "投递不存在")
	}
	if err != nil {
		return row, notificationDBError(err)
	}
	if !deliveryRetryable(row, m.now().UTC()) {
		return row, errs.New(10004, 409, "投递不可重试")
	}
	result := q.Table("notify_outbox").Where("tenant_id=? AND id=? AND status IN ('failed','channel_unavailable','pending','processing')", a.TenantID, id).
		Update("status", "pending")
	if result.Error != nil {
		return row, notificationDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return row, errs.New(10004, 409, "投递已被处理")
	}
	result = q.Table("notify_delivery").Where("tenant_id=? AND id=? AND status=?", a.TenantID, id, row.Status).
		Updates(map[string]any{"status": "pending", "next_retry_at": nil})
	if result.Error != nil {
		return row, notificationDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return row, errs.New(10004, 409, "投递已被处理")
	}
	row.Status, row.NextRetryAt = "pending", nil
	return row, m.record(ctx, a, "notification.delivery.retry", id, nil, map[string]any{"status": "pending"})
}

func deliveryRetryable(row channel.Delivery, now time.Time) bool {
	switch row.Status {
	case "failed", "skipped":
		return true
	case "pending":
		return row.Attempts == 0 && row.CreatedAt.Before(now.Add(-5*time.Minute)) &&
			(row.NextRetryAt == nil || !row.NextRetryAt.After(now))
	default:
		return false
	}
}
