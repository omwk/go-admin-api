package bootstrap

import (
	"context"
	"errors"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm/clause"
)

func (m *notificationManager) ListPersonalChannels(ctx context.Context, a channel.Actor) ([]channel.PersonalChannelView, error) {
	rows, err := m.store.personalChannels(ctx, a.TenantID, a.UserID)
	if err != nil {
		return nil, err
	}
	views := make([]channel.PersonalChannelView, 0, len(rows))
	for _, row := range rows {
		view, err := m.store.personalChannelView(row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (m *notificationManager) SavePersonalChannel(ctx context.Context, a channel.Actor, kind string, in channel.PersonalChannelInput, op *idem.Operation) (idem.Result, error) {
	if err := m.delivery.Personal.Validate(ctx, kind, in.Endpoint); err != nil {
		return idem.Result{}, errs.New(10001, 400, err.Error())
	}
	sealed, err := channel.SealJSON(m.sealer, channel.PersonalChannelAAD(a.TenantID, a.UserID, kind), in.Endpoint)
	if err != nil {
		return idem.Result{}, errs.New(10002, 500, "个人渠道加密不可用")
	}
	return m.Write(ctx, a, op, 200, false, func(tx context.Context) (any, error) {
		now := m.now().UTC()
		row := userNotificationChannel{TenantID: a.TenantID, UserID: a.UserID, Channel: kind, EndpointEnc: sealed, Enabled: in.Enabled == nil || *in.Enabled, CreatedAt: now, UpdatedAt: now}
		q, cancel := m.store.database.WithContext(tx)
		defer cancel()
		err := q.Table("sys_user_channel").Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "channel"}},
			DoUpdates: clause.AssignmentColumns([]string{"endpoint_enc", "verified_at", "enabled", "updated_at"})}, clause.Returning{}).Create(&row).Error
		if err != nil {
			return nil, notificationDBError(err)
		}
		view, err := m.store.personalChannelView(row)
		if err != nil {
			return nil, err
		}
		return view, m.record(tx, a, "notification.personal.save", row.ID, nil, view)
	})
}

func (m *notificationManager) DeletePersonalChannel(ctx context.Context, a channel.Actor, kind string, op *idem.Operation) (idem.Result, error) {
	if !channel.PersonalChannel(kind) {
		return idem.Result{}, errs.New(10001, 400, "个人渠道无效")
	}
	return m.Write(ctx, a, op, 204, false, func(tx context.Context) (any, error) {
		row, err := m.store.personalChannel(tx, a.TenantID, a.UserID, kind)
		if err != nil {
			return nil, err
		}
		q, cancel := m.store.database.WithContext(tx)
		defer cancel()
		result := q.Table("sys_user_channel").Where("tenant_id=? AND user_id=? AND channel=? AND id=?", a.TenantID, a.UserID, kind, row.ID).Delete(&userNotificationChannel{})
		if result.Error != nil {
			return nil, notificationDBError(result.Error)
		}
		if result.RowsAffected != 1 {
			return nil, errs.New(10003, 404, "个人渠道未配置")
		}
		return nil, m.record(tx, a, "notification.personal.delete", row.ID, row, nil)
	})
}

func (m *notificationManager) TestPersonalChannel(ctx context.Context, a channel.Actor, kind string, op *idem.Operation) (idem.Result, error) {
	if !channel.PersonalChannel(kind) {
		return idem.Result{}, errs.New(10001, 400, "个人渠道无效")
	}
	return m.Write(ctx, a, op, 200, true, func(tx context.Context) (any, error) {
		row, err := m.store.personalChannel(tx, a.TenantID, a.UserID, kind)
		if err != nil {
			return nil, err
		}
		if err := m.testRate(tx, a.TenantID); err != nil {
			return nil, err
		}
		d, err := m.delivery.Enqueue(tx, a.TenantID, channel.Request{Channel: kind, UserID: a.UserID, TemplateCode: "notification.test", Test: true, EndpointRevision: personalEndpointRevision(row)})
		if err != nil {
			return nil, err
		}
		return d, m.record(tx, a, "notification.personal.test", row.ID, nil, map[string]any{"delivery_id": d.ID})
	})
}

func (m *notificationManager) personalTestRecipient(ctx context.Context, tenant int64, in channel.Request) (string, error) {
	row, err := m.store.personalChannel(ctx, tenant, in.UserID, in.Channel)
	if err != nil || in.EndpointRevision == "" || personalEndpointRevision(row) != in.EndpointRevision {
		return "", errors.New("personal destination changed")
	}
	return m.store.openPersonalEndpoint(row)
}

func (m *notificationManager) personalChannelSent(ctx context.Context, tenant int64, in channel.Request) error {
	row, err := m.store.personalChannel(ctx, tenant, in.UserID, in.Channel)
	if err != nil {
		if errs.From(err).HTTPStatus == 404 {
			return channel.ErrPersonalDestinationChanged
		}
		return err
	}
	if personalEndpointRevision(row) != in.EndpointRevision {
		return channel.ErrPersonalDestinationChanged
	}
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	result := q.Table("sys_user_channel").Where("tenant_id=? AND user_id=? AND channel=? AND endpoint_enc=?", tenant, in.UserID, in.Channel, row.EndpointEnc).
		Updates(map[string]any{"verified_at": m.now().UTC(), "updated_at": m.now().UTC()})
	if result.Error != nil {
		return notificationDBError(result.Error)
	}
	if result.RowsAffected != 1 {
		return channel.ErrPersonalDestinationChanged
	}
	return nil
}
