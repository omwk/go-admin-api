package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/notifier/channel"
	"gorm.io/gorm"
)

const personalChannelColumns = "c.id,c.tenant_id,c.user_id,c.channel,c.endpoint_enc,c.verified_at,c.enabled,c.created_at,c.updated_at"

type userNotificationChannel struct {
	ID, TenantID, UserID int64
	Channel              string
	EndpointEnc          []byte
	VerifiedAt           *time.Time
	Enabled              bool
	CreatedAt, UpdatedAt time.Time
}

func (s *notificationStore) personalChannels(ctx context.Context, tenant, user int64) ([]userNotificationChannel, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	rows := []userNotificationChannel{}
	err := q.Table("sys_user_channel c").Select(personalChannelColumns).
		Joins("JOIN sys_user u ON u.tenant_id=c.tenant_id AND u.id=c.user_id AND u.status IN ('active','locked') AND u.deleted_at IS NULL").
		Where("c.tenant_id=? AND c.user_id=?", tenant, user).Order("c.channel").Find(&rows).Error
	return rows, notificationDBError(err)
}

func (s *notificationStore) personalChannel(ctx context.Context, tenant, user int64, kind string) (userNotificationChannel, error) {
	q, cancel := s.database.WithContext(ctx)
	defer cancel()
	var row userNotificationChannel
	err := q.Table("sys_user_channel c").Select(personalChannelColumns).
		Joins("JOIN sys_user u ON u.tenant_id=c.tenant_id AND u.id=c.user_id AND u.status IN ('active','locked') AND u.deleted_at IS NULL").
		Where("c.tenant_id=? AND c.user_id=? AND c.channel=?", tenant, user, kind).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, errs.New(10003, 404, "个人渠道未配置")
	}
	return row, notificationDBError(err)
}

func (s *notificationStore) openPersonalEndpoint(row userNotificationChannel) (string, error) {
	var endpoint string
	err := channel.OpenJSON(s.sealer, channel.PersonalChannelAAD(row.TenantID, row.UserID, row.Channel), row.EndpointEnc, &endpoint)
	if err != nil {
		return "", errs.New(10002, 500, "个人渠道不可用")
	}
	return endpoint, nil
}

func (s *notificationStore) personalChannelView(row userNotificationChannel) (channel.PersonalChannelView, error) {
	endpoint, err := s.openPersonalEndpoint(row)
	if err != nil {
		return channel.PersonalChannelView{}, err
	}
	return channel.PersonalChannelView{Channel: row.Channel, EndpointMasked: channel.MaskRecipient(row.Channel, endpoint), Enabled: row.Enabled,
		VerifiedAt: row.VerifiedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func personalEndpointRevision(row userNotificationChannel) string {
	sum := sha256.Sum256(row.EndpointEnc)
	return hex.EncodeToString(sum[:])
}
