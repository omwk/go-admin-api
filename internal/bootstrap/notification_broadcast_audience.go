package bootstrap

import (
	"context"
	"slices"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
)

type broadcastRecipient struct{ TenantID, ID int64 }

func normalizeBroadcastAudience(in channel.Audience) (channel.Audience, error) {
	if in.Type != "all" && in.Type != "roles" && in.Type != "users" {
		return in, errs.New(10001, 400, "公告范围类型无效")
	}
	if in.Type == "all" && len(in.IDs) != 0 || in.Type != "all" && len(in.IDs) == 0 {
		return in, errs.New(10001, 400, "公告范围编号无效")
	}
	in.IDs = slices.Clone(in.IDs)
	slices.Sort(in.IDs)
	in.IDs = slices.Compact(in.IDs)
	for _, id := range in.IDs {
		if id <= 0 {
			return in, errs.New(10001, 400, "公告范围编号无效")
		}
	}
	return in, nil
}

func (m *notificationManager) broadcastCrossTenant(a channel.Actor) bool {
	return a.Super && a.TenantID == m.platformTenantID
}

func (m *notificationManager) broadcastAudience(ctx context.Context, a channel.Actor, in channel.Audience) (*gorm.DB, context.CancelFunc, error) {
	q, cancel := m.store.database.WithContext(ctx)
	if in.Type != "all" {
		if err := m.checkBroadcastReferences(q, a, in); err != nil {
			return nil, cancel, err
		}
	}
	users := q.Table("sys_user u").Where("u.status='active' AND u.deleted_at IS NULL")
	if !m.broadcastCrossTenant(a) {
		users = users.Where("u.tenant_id=?", a.TenantID)
	}
	switch in.Type {
	case "users":
		users = users.Where("u.id IN ?", in.IDs)
	case "roles":
		users = users.Where(`EXISTS (SELECT 1 FROM sys_user_role ur JOIN sys_role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id
 WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND r.status='active' AND r.id IN ?)`, in.IDs)
	}
	return users, cancel, nil
}

func (m *notificationManager) checkBroadcastReferences(q *gorm.DB, a channel.Actor, in channel.Audience) error {
	table, filter := "sys_user", "deleted_at IS NULL AND status='active'"
	if in.Type == "roles" {
		table, filter = "sys_role", "status='active'"
	}
	refs := q.Table(table).Where("id IN ?", in.IDs).Where(filter)
	if !m.broadcastCrossTenant(a) {
		refs = refs.Where("tenant_id=?", a.TenantID)
	}
	var count int64
	if err := refs.Count(&count).Error; err != nil {
		return notificationDBError(err)
	}
	if count != int64(len(in.IDs)) {
		return errs.New(10003, 404, "公告范围不存在")
	}
	return nil
}

func (m *notificationManager) PreviewBroadcast(ctx context.Context, a channel.Actor, in channel.Audience) (any, error) {
	in, err := normalizeBroadcastAudience(in)
	if err != nil {
		return nil, err
	}
	q, cancel, err := m.broadcastAudience(ctx, a, in)
	defer cancel()
	if err != nil {
		return nil, err
	}
	var count int64
	err = q.Count(&count).Error
	return struct {
		Recipients int64 `json:"recipients"`
	}{count}, notificationDBError(err)
}
