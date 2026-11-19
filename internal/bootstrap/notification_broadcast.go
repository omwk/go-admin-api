package bootstrap

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
)

type broadcastPayload struct {
	channel.BroadcastInput
	Recipients int64 `json:"recipients"`
}

type broadcastRow struct {
	ID                                int64
	TenantID                          int64
	Channel, Target, Template, Status string
	Payload                           json.RawMessage `gorm:"type:jsonb"`
	CreatedAt                         time.Time
	SentAt                            *time.Time
}

func validateBroadcast(in channel.BroadcastInput) (channel.BroadcastInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || strings.ContainsAny(in.Title, "\r\n") || utf8.RuneCountInString(in.Title) > 128 || strings.TrimSpace(in.Body) == "" || len(in.Body) > 128*1024 {
		return in, errs.New(10001, 400, "公告标题或正文无效")
	}
	if len(in.Channels) == 0 || len(in.Channels) > 4 {
		return in, errs.New(10001, 400, "公告渠道无效")
	}
	seen := map[string]bool{}
	for _, kind := range in.Channels {
		if kind != "inbox" && kind != "email" && kind != "wecom" && kind != "dingtalk" || seen[kind] {
			return in, errs.New(10001, 400, "公告渠道无效")
		}
		seen[kind] = true
	}
	var err error
	in.Audience, err = normalizeBroadcastAudience(in.Audience)
	return in, err
}

func (m *notificationManager) CreateBroadcast(ctx context.Context, a channel.Actor, in channel.BroadcastInput) (any, error) {
	in, err := validateBroadcast(in)
	if err != nil {
		return nil, err
	}
	var result channel.BroadcastResult
	err = m.store.Transaction(ctx, func(tx context.Context) error {
		query, cancel, err := m.broadcastAudience(tx, a, in.Audience)
		defer cancel()
		if err != nil {
			return err
		}
		recipients, err := m.enqueueBroadcastAudience(tx, query, in)
		if err != nil {
			return err
		}
		result.Recipients = recipients
		if err := m.enqueueBroadcastRobots(tx, query, in); err != nil {
			return err
		}
		q, cancelWrite := m.store.database.WithContext(tx)
		defer cancelWrite()
		payload, err := json.Marshal(broadcastPayload{BroadcastInput: in, Recipients: result.Recipients})
		if err != nil {
			return err
		}
		now := m.now().UTC()
		history := broadcastRow{TenantID: a.TenantID, Channel: "inbox", Target: "broadcast", Template: "notification.broadcast",
			Status: "sent", Payload: payload, CreatedAt: now, SentAt: &now}
		if err := q.Table("notification").Create(&history).Error; err != nil {
			return notificationDBError(err)
		}
		result.BroadcastID = history.ID
		return m.record(tx, a, "system.notify.broadcast", result.BroadcastID, nil, map[string]any{
			"title": in.Title, "channels": in.Channels, "audience": in.Audience, "recipients": result.Recipients})
	})
	return result, err
}

func (m *notificationManager) enqueueBroadcastRobots(ctx context.Context, query *gorm.DB, in channel.BroadcastInput) error {
	if !slices.Contains(in.Channels, "wecom") && !slices.Contains(in.Channels, "dingtalk") {
		return nil
	}
	var tenants []int64
	if err := query.Session(&gorm.Session{}).Distinct("u.tenant_id").Pluck("u.tenant_id", &tenants).Error; err != nil {
		return notificationDBError(err)
	}
	for _, tenant := range tenants {
		for _, kind := range in.Channels {
			if kind != "wecom" && kind != "dingtalk" {
				continue
			}
			if _, err := m.delivery.Enqueue(ctx, tenant, channel.Request{Channel: kind, TemplateCode: "notification.broadcast", Target: "broadcast",
				Variables: map[string]string{"Title": in.Title, "Body": in.Body}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *notificationManager) enqueueBroadcastAudience(ctx context.Context, query *gorm.DB, in channel.BroadcastInput) (int64, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	var last, total int64
	for {
		users := []broadcastRecipient{}
		err := query.Session(&gorm.Session{}).Select("u.tenant_id,u.id").Where("u.id>?", last).Order("u.id").Limit(500).Find(&users).Error
		if err != nil {
			return 0, notificationDBError(err)
		}
		if err := m.enqueueBroadcast(ctx, q, in, users); err != nil {
			return 0, err
		}
		total += int64(len(users))
		if len(users) < 500 {
			return total, nil
		}
		last = users[len(users)-1].ID
	}
}

func (m *notificationManager) enqueueBroadcast(ctx context.Context, q *gorm.DB, in channel.BroadcastInput, users []broadcastRecipient) error {
	payload, err := json.Marshal(map[string]string{"title": in.Title, "body": in.Body})
	if err != nil {
		return err
	}
	now := m.now().UTC()
	if slices.Contains(in.Channels, "inbox") {
		rows := make([]broadcastRow, 0, len(users))
		for _, u := range users {
			rows = append(rows, broadcastRow{TenantID: u.TenantID, Channel: "inbox", Target: "user:" + fmtNotificationID(u.ID),
				Template: "notification.broadcast", Status: "sent", Payload: payload, CreatedAt: now, SentAt: &now})
		}
		if len(rows) > 0 {
			if err := q.Table("notification").CreateInBatches(&rows, 500).Error; err != nil {
				return notificationDBError(err)
			}
		}
	}
	if slices.Contains(in.Channels, "email") {
		for _, u := range users {
			if _, err := m.delivery.Enqueue(ctx, u.TenantID, channel.Request{Channel: "email", TemplateCode: "notification.broadcast",
				UserID: u.ID, Variables: map[string]string{"Title": in.Title, "Body": in.Body}}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *notificationManager) ListBroadcasts(ctx context.Context, a channel.Actor, page, size int) (any, error) {
	q, cancel := m.store.database.WithContext(ctx)
	defer cancel()
	query := q.Table("notification").Where("tenant_id=? AND target='broadcast' AND template='notification.broadcast' AND channel='inbox'", a.TenantID)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, notificationDBError(err)
	}
	rows := []broadcastRow{}
	if err := query.Select("id,payload,created_at").Order("created_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, notificationDBError(err)
	}
	views := make([]channel.BroadcastView, 0, len(rows))
	for _, row := range rows {
		var payload broadcastPayload
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			return nil, errs.New(10002, 500, "公告记录无效")
		}
		views = append(views, channel.BroadcastView{BroadcastResult: channel.BroadcastResult{BroadcastID: row.ID, Recipients: payload.Recipients},
			Title: payload.Title, Channels: payload.Channels, Audience: payload.Audience, CreatedAt: row.CreatedAt})
	}
	return channel.Page{List: views, Total: total, Page: page, PageSize: size}, nil
}
