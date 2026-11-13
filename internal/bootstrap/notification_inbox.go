package bootstrap

import (
	"context"

	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
)

func (n *credentialNotifications) UnreadCount(ctx context.Context, tenant, user int64) (int64, error) {
	q, cancel, err := n.inboxQuery(ctx, tenant, user)
	defer cancel()
	if err != nil {
		return 0, err
	}
	var count int64
	err = q.Where("n.read_at IS NULL").Count(&count).Error
	return count, credentialDBError(err)
}

func (n *credentialNotifications) ReadAll(ctx context.Context, tenant, user int64) (int64, error) {
	q, cancel, err := n.inboxQuery(ctx, tenant, user)
	defer cancel()
	if err != nil {
		return 0, err
	}
	result := q.Where("n.read_at IS NULL").Update("read_at", n.now().UTC())
	return result.RowsAffected, credentialDBError(result.Error)
}

func (n *credentialNotifications) Write(ctx context.Context, tenant, user int64, op *idem.Operation, fn func(context.Context) (any, error)) (idem.Result, error) {
	m := newNotificationManager(&notificationStore{database: n.runtime.database}, n.runtime.sealer, n.now)
	return m.Write(ctx, channel.Actor{TenantID: tenant, UserID: user}, op, 200, false, fn)
}
