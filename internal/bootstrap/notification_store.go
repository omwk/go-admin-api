package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/db"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const providerColumns = "id,tenant_id,channel,kind,name,config,enabled,priority,created_at,updated_at"
const templateColumns = "id,tenant_id,code,locale,subject,body,format,enabled"
const deliveryColumns = "id,tenant_id,channel,template_code,recipient_hash,recipient_domain,provider_id,status,attempts,last_error,created_at,sent_at,next_retry_at"

type notificationStore struct{ database *db.DB }

func (s *notificationStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	return notificationDBError(s.database.Transaction(ctx, fn))
}

func notificationDBError(err error) error {
	if err == nil {
		return nil
	}
	var app *errs.AppError
	if errors.As(err, &app) {
		return err
	}
	return errs.New(10002, 500, "通知存储操作失败")
}

func (s *notificationStore) Providers(ctx context.Context, tenant int64, kind string) ([]channel.Provider, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []channel.Provider{}
	err := q.Table("channel_provider").Select(providerColumns).Where("(tenant_id=? OR tenant_id IS NULL) AND channel=? AND enabled", tenant, kind).Order("priority,id").Find(&rows).Error
	return rows, notificationDBError(err)
}

func (s *notificationStore) Template(ctx context.Context, tenant int64, code, locale string) (channel.Template, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row channel.Template
	err := q.Table("notify_template").Select(templateColumns).Where("(tenant_id=? OR tenant_id IS NULL) AND code=? AND locale=?", tenant, code, locale).Order("tenant_id NULLS LAST").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, nil
	}
	return row, notificationDBError(err)
}

func (s *notificationStore) Recipient(ctx context.Context, tenant, user int64, kind string) (string, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row struct{ Email, Phone *string }
	err := q.Table("sys_user").Select("email,phone").Where("tenant_id=? AND id=? AND status IN ('active','locked') AND deleted_at IS NULL", tenant, user).Take(&row).Error
	if err != nil {
		return "", errors.New("notification recipient unavailable")
	}
	if kind != "email" && kind != "sms" {
		return "", nil
	}
	if kind == "email" && row.Email != nil {
		return *row.Email, nil
	}
	if kind == "sms" && row.Phone != nil {
		return *row.Phone, nil
	}
	return "", errors.New("notification recipient unavailable")
}

func (s *notificationStore) Insert(ctx context.Context, d *channel.Delivery, o *channel.Outbox) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	if d.ID == 0 {
		return notificationDBError(q.Table("notify_delivery").Create(d).Error)
	}
	o.ID = d.ID
	return notificationDBError(q.Table("notify_outbox").Create(o).Error)
}

func (s *notificationStore) Claim(ctx context.Context, tenant, id int64, now time.Time) (channel.Delivery, channel.Outbox, bool, error) {
	var d channel.Delivery
	var o channel.Outbox
	found := false
	err := s.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		query := q.Table("notify_delivery d").Select("d."+deliveryColumnsWithPrefix()).Joins("JOIN notify_outbox o ON o.id=d.id AND o.tenant_id=d.tenant_id").
			Where("o.status IN ('pending','processing','channel_unavailable') AND (d.next_retry_at IS NULL OR d.next_retry_at<=?)", now)
		if tenant > 0 {
			query = query.Where("d.tenant_id=?", tenant)
		}
		if id > 0 {
			query = query.Where("d.id=?", id)
		}
		err := query.Order("d.id").Clauses(clause.Locking{Strength: "UPDATE", Table: clause.Table{Name: "d"}, Options: "SKIP LOCKED"}).Take(&d).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.Table("notify_outbox").Select("id,tenant_id,channel,template_code,payload,status").Where("tenant_id=? AND id=?", d.TenantID, d.ID).Take(&o).Error; err != nil {
			return err
		}
		lease := now.Add(3 * time.Minute)
		d.Status, d.NextRetryAt = "pending", &lease
		if err := q.Table("notify_delivery").Where("tenant_id=? AND id=?", d.TenantID, d.ID).Updates(map[string]any{"status": "pending", "next_retry_at": lease}).Error; err != nil {
			return err
		}
		found = true
		return q.Table("notify_outbox").Where("tenant_id=? AND id=?", d.TenantID, d.ID).Update("status", "processing").Error
	})
	return d, o, found, err
}

func deliveryColumnsWithPrefix() string {
	return "id,d.tenant_id,d.channel,d.template_code,d.recipient_hash,d.recipient_domain,d.provider_id,d.status,d.attempts,d.last_error,d.created_at,d.sent_at,d.next_retry_at"
}

func (s *notificationStore) Finish(ctx context.Context, d channel.Delivery, lease time.Time, status string, alert bool) error {
	return s.Transaction(ctx, func(tx context.Context) error {
		q, cancel := s.database.WithContext(tx)
		defer cancel()
		var next *time.Time
		if status == "channel_unavailable" {
			at := lease.Add(2 * time.Minute)
			next = &at
		}
		result := q.Table("notify_delivery").Where("tenant_id=? AND id=? AND next_retry_at=? AND status='pending'", d.TenantID, d.ID, lease).
			Updates(map[string]any{"provider_id": d.ProviderID, "recipient_hash": d.RecipientHash, "recipient_domain": d.RecipientDomain, "status": d.Status, "attempts": d.Attempts, "last_error": d.LastError, "sent_at": d.SentAt, "next_retry_at": next})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errs.New(10004, 409, "投递已由其他任务处理")
		}
		if err := q.Table("notify_outbox").Where("tenant_id=? AND id=?", d.TenantID, d.ID).Update("status", status).Error; err != nil {
			return err
		}
		if alert {
			return s.administratorAlert(tx, d)
		}
		return nil
	})
}

func (s *notificationStore) administratorAlert(ctx context.Context, d channel.Delivery) error {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	// Resolve current administrators in this tenant; the payload contains IDs only.
	return q.Exec(`INSERT INTO notification(tenant_id,channel,target,template,payload,status,dedup_key,sent_at)
 SELECT u.tenant_id,'inbox','user:'||u.id::text,'notification.delivery_failed',jsonb_build_object('delivery_id',?::bigint),'sent',?||':'||u.id::text,now()
 FROM sys_user u WHERE u.tenant_id=? AND u.status='active' AND u.deleted_at IS NULL
 AND (u.is_super OR EXISTS(SELECT 1 FROM sys_user_role ur JOIN sys_role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.status='active'
 JOIN sys_role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id
 JOIN sys_permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.code='system:setting:write'
 WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id))
 ON CONFLICT(tenant_id,dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING`, d.ID, fmt.Sprintf("notify-delivery:%d", d.ID), d.TenantID).Error
}
